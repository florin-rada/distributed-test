package mymain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"regexp"
	"sync/atomic"
	"time"
)

// FILENAME the name of the local "db"
const FILENAME = "local.db"

// CONFIGNAME holds the name of the config file used at startup
const CONFIGNAME = "config.cfg"

const (
	// FOLLOWER state of the node
	FOLLOWER = 1
	//CANDIDATE state of the node
	CANDIDATE = 2
	//LEADER state of the node
	LEADER = 3
)

var (
	// WarningLogger writes all warnings to the log
	WarningLogger *log.Logger
	// InfoLogger writes all info (access, arguments, responses) to the log
	InfoLogger *log.Logger
	// ErrorLogger writes all errors to the log
	ErrorLogger *log.Logger

	PATH_IS_DIRECTORY = errors.New("Path is directory")
)

const (
	// ActionGet enum value sent to StoreManager should return value based on key
	ActionGet = iota
	// ActionSet enum value sent to StoreManager when it should Add the value to the store
	ActionSet
	// ActionSyncLocal enum value sent to StoreManager when it should write the Store to file
	ActionSyncLocal

	// ActionUpdateFromNode enum value sent to StoreManager when updating local db with value from node
	ActionUpdateFromNode

	// ActionSyncLogFromNode enum value sent to storeManager when the local db should be discarded and all values replaced by data received from node
	ActionSyncLogFromNode
	// ActionExit enum value sent to StoreManager when the function should exit
	ActionExit
)

const (
	// LogInfo INFO level logging
	LogInfo = iota
	// LogWarning Warning level logging
	LogWarning
	// LogError Error level loging
	LogError
)

// NodeInfo represents the details used to connect to other nodes
type NodeInfo struct {
	HTTPAddress string `json:"HTTPAddress"`
	RPCAddress  string `json:"RPCAddress"`
}

// Config contains the details used at the startup of the node
type Config struct {
	HTTPPort string     `json:"HTTPPort"`
	RPCPort  string     `json:"RPCPort"`
	Nodes    []NodeInfo `json:"Nodes"`
}

// RespWriterWithCode is a version of response writer that stores the StatusCode
type RespWriterWithCode struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

// Message instance will be sent via channel to the UpdateStore coroutine to be added to the store
type Message struct {
	Text   string         `json:"Text"`
	Counts map[string]int `json:"Counts"`
}

// Action is sent to the StoreManager and depending on the supplied values specific actions will be set
type Action struct {
	Action       int
	MSG          Message
	ReplyChannel chan Message
	Index        uint64 // only sent with ActionSyncLogFromNode this sets the new index to this value
}

// Response is the struct returned by all api calls (under json format)
type Response struct {
	Errors   []string    `json:"errors"`
	Response interface{} `json:"response"`
}

// Nodes are the paths/urls to all the nodes available
var Nodes []string

// Store is the new type to hold our mock db
// We create a type for it so we can use it in the RPC to sync the nodes
type Store map[string]int

// Index represents the number of modifications done to the store, it's used to sync with the other nodes
var Index uint64

// InternalStore is the in memory "db" instance
var InternalStore Store

// StoreChannel will be used to communicate with the Store, all writes will be done through it
var StoreChannel chan Action

// CFG holds the configuration values for starting the node
var CFG Config

func main() {
	rpc.Register(&InternalStore)
	rpc.HandleHTTP()
	listener, err := net.Listen("tcp", CFG.RPCPort)
	if err != nil {
		log.Fatalf("Listen error for RPC: %s", err.Error())
		return
	}
	go http.Serve(listener, nil)
	go StoreManager()  // coroutine managing the in memory store
	go LocalSyncTick() // coroutine telling the store manager to sync with local
	go HeartbeatTick()
	http.HandleFunc("/", MiddleWere)
	http.ListenAndServe(CFG.HTTPPort, nil)
}

// NewResponse creates a new response to be returned
func NewResponse() *Response {
	resp := Response{
		Errors:   make([]string, 0),
		Response: "",
	}
	return &resp
}

// MiddleWere will handle the base api call and based on the type of call execute the appropriate function
func MiddleWere(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		PostContent(w, r)
	} else if r.Method == "GET" {
		GetContent(w, r)
	} else {
		log.Fatalf("Unknown Request received.")
	}
}

// GetContent handles the GET api call that returns the content of our mockup Db
func GetContent(w http.ResponseWriter, r *http.Request) {
	resp := NewResponse()
	err := r.ParseForm()
	if err != nil {
		resp.Errors = append(resp.Errors, err.Error())
		WriteJSON(w, resp)
		return
	}

	value := r.FormValue("text")
	if value == "" {
		resp.Errors = append(resp.Errors, "No text supplied.")
		WriteJSON(w, resp)
		return
	}
	responseChannel := make(chan Message, 1) // we use a buffered channel so we don't block the StoreManager untill our call gets some cpu time to read the value
	act := Action{
		ActionGet,
		Message{value, map[string]int{}},
		responseChannel,
		0,
	}
	StoreChannel <- act           // sending request for our data to the store manager
	response := <-responseChannel // waiting for a reply
	if response.Text == "" {
		resp.Errors = append(resp.Errors, "Error, no result for supplied text")
		WriteJSON(w, resp)
		return
	}
	resp.Response = response
	WriteJSON(w, resp)
}

// PostContent handles the POST that adds a new entry in our mockup DB
func PostContent(w http.ResponseWriter, r *http.Request) {
	resp := NewResponse()
	err := r.ParseForm()
	if err != nil {
		resp.Errors = append(resp.Errors, err.Error())
		WriteJSON(w, resp)
		return
	}
	value := r.FormValue("text")
	if value == "" {
		resp.Errors = append(resp.Errors, "No text supplied.")
		WriteJSON(w, resp)
		return
	}
	// regex to split the text into words
	tokenizer := regexp.MustCompile("[ \t,.!?;]{1,}")
	words := tokenizer.Split(value, -1)
	wordCount := make(map[string]int)
	for _, word := range words {
		wordCount[word]++
	}
	msg := Message{
		value,
		wordCount,
	}
	act := Action{
		ActionSet,
		msg,
		nil,
		0,
	}
	StoreChannel <- act
	resp.Response = wordCount
	WriteJSON(w, resp)
}

// StoreManager will run in a coroutine and will update the Store or return values from store
func StoreManager() {
	modified := false
	for {
		action := <-StoreChannel
		switch action.Action {
		case ActionGet:
			{
				tokenizer := regexp.MustCompile("[ \t,.!?;]{1,}")
				words := tokenizer.Split(action.MSG.Text, -1)
				counts := make(map[string]int)
				for _, word := range words {
					if num, ok := InternalStore[word]; ok {
						counts[word] = num
					} else {
						counts[word] = 0
					}
				}

				msg := Message{
					action.MSG.Text,
					counts,
				}
				action.ReplyChannel <- msg
				break
			}
		case ActionSet:
			{
				for word, count := range action.MSG.Counts {
					InternalStore[word] += count
				}
				modified = true
				atomic.AddUint64(&Index, 1)
				// syncing with other nodes
				action.Action = ActionUpdateFromNode
				for _, nodePath := range CFG.Nodes {
					conn, err := rpc.DialHTTP("tcp", nodePath.RPCAddress)
					if err != nil {
						LogEvent(fmt.Sprintf("Error connecting to node for sync: %s", err.Error()), LogError)
						continue
					}
					var dummyInt uint64 = 0
					err = conn.Call("Store.AppendEntry", &action, &dummyInt)
					if err != nil {
						LogEvent(fmt.Sprintf("Error sending data to node %s: %s", nodePath, err.Error()), LogError)
						continue
					}
					defer conn.Close()
				}
				break
			}
		case ActionUpdateFromNode:
			{
				for word, count := range action.MSG.Counts {
					InternalStore[word] += count
				}
				modified = true
				atomic.AddUint64(&Index, 1)
				break
			}
		case ActionSyncLocal:
			{
				if modified { // we update the local file only if we have modifications in our in memory store
					UpdateFileContent(InternalStore)
					modified = false
				}
			}
		case ActionSyncLogFromNode:
			{
				InternalStore = make(Store)
				for word, count := range action.MSG.Counts {
					InternalStore[word] = count
				}
				atomic.StoreUint64(&Index, action.Index)
				modified = true
				break
			}
		case ActionExit:
			{
				close(StoreChannel)
				return
			}
		default:
			{
				fmt.Printf("Unknown action")
			}
		}
	}
}

// UpdateFileContent will update the local db (file) containing our mockup DB
func UpdateFileContent(store Store) {
	data := struct {
		Store Store  `json:"Store"`
		Index uint64 `json:"Index"`
	}{
		InternalStore,
		atomic.LoadUint64(&Index),
	}
	jsonFile, err := json.Marshal(data)
	if err != nil {
		fmt.Printf("Error // to handle%s", err.Error())
		return
	}
	file, err := os.OpenFile(FILENAME, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Printf("Error opening local db %s", err.Error())
		return
	}
	if _, err := file.Write(jsonFile); err != nil {
		file.Close()
		fmt.Printf("Error writing json file to disk: %s", err.Error())
		return
	}
	if err := file.Close(); err != nil {
		fmt.Printf("Error saving file to disk %s", err.Error())
		return
	}
}

// ReadFileContent will read the local db (file) and fill the "in memory" db (the variable holding the data)
func ReadFileContent(fileName string) error {
	info, err := os.Stat(fileName)
	if os.IsNotExist(err) {
		return err
	}
	if info.IsDir() {
		return PATH_IS_DIRECTORY
	}
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}
	data := struct {
		Store Store  `json:"Store"`
		Index uint64 `json:"Index"`
	}{
		InternalStore,
		Index,
	}
	if len(fileContent) <= 0 {
		return nil
	}
	err = json.Unmarshal(fileContent, &data)
	if err != nil {
		return err
	}
	InternalStore = data.Store
	atomic.StoreUint64(&Index, data.Index)
	return nil
}

// WriteJSON writes the object as json to the client
func WriteJSON(w http.ResponseWriter, obj interface{}) {
	jsonObj, err := json.Marshal(obj)
	if err != nil {
		log.Fatalf("Error writing json to response %s", err.Error())
		return
	}
	w.Write(jsonObj)
}

// LocalSyncTick will send a message to the StoreManager to update the local db file from time to time
func LocalSyncTick() {
	for {
		time.Sleep(time.Second * 1)
		act := Action{
			ActionSyncLocal,
			Message{},
			nil,
			0,
		}
		StoreChannel <- act
	}
}

// LogEvent writes the string received to the log file with the specified level
func LogEvent(str string, level int) {
	switch level {
	case LogInfo:
		{
			InfoLogger.Println(str)
			break
		}
	case LogWarning:
		{
			WarningLogger.Println(str)
			break
		}
	case LogError:
		{
			ErrorLogger.Println(str)
			break
		}
	default:
		{
			ErrorLogger.Printf("Error writing to know '%s', unknown log level", str)
		}
	}
}

// Status returns the status code of the response
func (rw RespWriterWithCode) Status() int {
	return rw.statusCode
}

// WriteHeader is a new implementation of http.ResponseWriter WriteHeader that stores the status code
func (rw *RespWriterWithCode) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
	rw.wroteHeader = true
}

// AppendEntry is a RPC call to append new data to our store from other node
func (s *Store) AppendEntry(arg *Action, unusedRetVal *uint64) error {
	StoreChannel <- *arg
	return nil
}

// GetLogIndex returns the Index number of the local Store at the request of another node for sync
func (s Store) GetLogIndex(arg Action, retIndex *uint64) error {
	*retIndex = atomic.LoadUint64(&Index)
	return nil
}

// GetLogSyncAction gets from the node all data for the purpose of rebuilding the local db from the node we are asking
func (s Store) GetLogSyncAction(arg Action, retAct *Action) error {
	*retAct = Action{
		ActionSyncLogFromNode,
		Message{
			"",
			InternalStore,
		},
		nil,
		atomic.LoadUint64(&Index),
	}
	return nil
}

// Heartbeat is called every few ticks to check if a node is still alive
// If the function can't be executed or connection can't be established we decide the node is down
func (s Store) Heartbeat(arg Action, reply *bool) error {
	*reply = true
	return nil
}

// SyncLog will update the local db by overwriting it with the one received from the node (this should only happen on startup when other nodes already have changes)
func (s *Store) SyncLog(arg Action, unused *uint64) error {
	StoreChannel <- arg
	return nil
}

// HeartbeatTick is ran in a coroutine and every established interval tries to connect to all the nodes
// If all nodes fail to connect it cosiders that itself is down and when connections will be established, it will ask for a full store update
func HeartbeatTick() {
	done := make(chan *rpc.Call, len(CFG.Nodes))
	var shouldRequestUpdate bool = true
	nodeStatus := make(map[int]bool)
	for {
		time.Sleep(time.Microsecond * 300)
		var failedCounter int = 0
		var executedConnections int = 0
		for idx, cltData := range CFG.Nodes {
			clt, err := rpc.DialHTTP("tcp", cltData.RPCAddress)
			if err != nil {
				nodeStatus[idx] = false
				failedCounter++
				continue
			}

			var repl bool
			clt.Go("Store.Heartbeat", Action{}, &repl, done)
			executedConnections++
			nodeStatus[idx] = true
		}
		var replyCounter int = 0
		for {
			resp := <-done
			replyCounter++
			if resp.Error != nil {
				LogEvent(fmt.Sprintf("Error getting heartbeat from node: %s", resp.Error.Error()), LogError)
			}
			if replyCounter == executedConnections {
				break
			}
		}
		if failedCounter == len(CFG.Nodes) {
			shouldRequestUpdate = true // it means we are either down or the only node available
			continue
		} else {
			// if previously we were down or the only node we compare with other node
			// we check local index against the index of a node
			// if our index is below the index of the node we ask for a full sync
			if shouldRequestUpdate {
				for idx, sts := range nodeStatus {
					if sts == true {
						clt, err := rpc.DialHTTP("tcp", CFG.Nodes[idx].RPCAddress)
						if err != nil {
							nodeStatus[idx] = false
							continue
						}
						var repl uint64
						err = clt.Call("Store.GetLogIndex", Action{}, &repl)
						if err != nil {
							nodeStatus[idx] = false
							continue
						}
						if repl > atomic.LoadUint64(&Index) {
							act := Action{}
							err = clt.Call("Store.GetLogSyncAction", Action{}, &act)
							if err != nil {
								nodeStatus[idx] = false
								continue
							}
							StoreChannel <- act
							shouldRequestUpdate = false
							break
						}
					}
				}
			}
		}
	}
}

func init() {
	Nodes = []string{}
	cfgFile, err := os.OpenFile(CONFIGNAME, os.O_RDONLY, 0666)
	if err != nil {
		log.Fatal(err.Error())
		return
	}
	cfgData, err := ioutil.ReadAll(cfgFile)
	if err != nil {
		log.Fatal(err.Error())
		return
	}
	err = json.Unmarshal(cfgData, &CFG)
	if err != nil {
		log.Fatal(err.Error())
		return
	}
	InternalStore = make(map[string]int)

	_, err = os.OpenFile(FILENAME, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil && !errors.Is(err, os.ErrExist) {
		log.Fatal(err.Error())
		return
	}
	err = ReadFileContent(FILENAME)
	if err != nil {
		log.Fatal(err.Error())
	}
	StoreChannel = make(chan Action, 1024)

	file, err := os.OpenFile("logs.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Fatal(err.Error())
		return
	}

	InfoLogger = log.New(file, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	WarningLogger = log.New(file, "WARNING: ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(file, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
}
