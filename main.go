package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/rpc"
	"os"
	"regexp"
	"time"
)

// FILENAME the name of the local "db"
const FILENAME = "local.db"
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

// RespWriterWithCode is a version of response writer that stores the StatusCode
type RespWriterWithCode struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

// Message instance will be sent via channel to the UpdateStore coroutine to be added to the store
type Message struct {
	Text   string
	Counts map[string]int
}

// Action is sent to the StoreManager and depending on the supplied values specific actions will be set
type Action struct {
	Action       int
	MSG          Message
	ReplyChannel chan Message
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

// InternalStore is the in memory "db" instance
var InternalStore Store

// StoreChannel will be used to communicate with the Store, all writes will be done through it
var StoreChannel chan Action

func main() {

	go StoreManager()  // coroutine managing the in memory store
	go LocalSyncTick() // coroutine telling the store manager to sync with local
	http.HandleFunc("/", MiddleWere)
	http.ListenAndServe(":8080", nil)
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
	} else if r.Method == "DELETE" {
		DeleteContent(w, r)
	}
}

// GetContent handles the POSt api call that returns the content of our mockup Db
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
	}
	StoreChannel <- act
	resp.Response = wordCount
	WriteJSON(w, resp)
}

// DeleteContent handles the DELETE call that removes a entry from our mockup DB
func DeleteContent(w http.ResponseWriter, r *http.Request) {

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
				// syncing with other nodes

				for _, nodePath := range Nodes {
					conn, err := rpc.DialHTTP("tcp", nodePath)
					if err != nil {
						LogEvent(fmt.Sprintf("Error connecting to node for sync: %s", err.Error()), LogError)
						continue
					}
					err = conn.Call("Action.AppendEntry", action, nil)
					if err != nil {
						LogEvent(fmt.Sprintf("Error sending data to node %s: %s", nodePath, err.Error()), LogError)
						continue
					}
				}
				break
			}
		case ActionUpdateFromNode:
			{
				for word, count := range action.MSG.Counts {
					InternalStore[word] += count
				}
				modified = true
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
	jsonFile, err := json.Marshal(InternalStore)
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
func ReadFileContent() {
	info, err := os.Stat(FILENAME)
	if os.IsNotExist(err) {
		return
	}
	if info.IsDir() {
		log.Fatal("Can't open file name as it is a director")
		return
	}
	file, err := os.Open(FILENAME)
	if err != nil {

	}
	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("Error reading file: %s", err.Error())

	}
	err = json.Unmarshal(fileContent, &InternalStore)
	if err != nil {
		log.Fatalf("Error unmarsheling json: %s", err.Error())
	}
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
func (s *Store) AppendEntry(arg Action) error {
	StoreChannel <- arg
	return nil
}

// SyncLog will update the local db by overwriting it with the one received from the node (this should only happen on startup when other nodes already have changes)
func (s *Store) SyncLog(arg Action) error {
	StoreChannel <- arg
	return nil
}

// PropagateNewEntry will send an update to all nodes available
func PropagateNewEntry() {

}

func init() {
	Nodes = []string{}
	InternalStore = make(map[string]int)
	ReadFileContent()
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
