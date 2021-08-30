package mymain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestReadFileContent(t *testing.T) {
	validDataTest := "Test Valid Data"
	tests := []struct {
		name string
		path string
	}{
		{validDataTest,
			"tests_data/readFileContentTestData.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("Testing: \"%s\" for path \"%s\"\n", tt.name, tt.path)
			err := ReadFileContent(tt.path)
			if err != nil {
				t.Fatalf("Test fails, expected nill, received: %s", err.Error())
			}
		})
	}
}

func TestReadFileContentFails(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected error
	}{
		{"Invalid file path", "tests_data/InvalidPath.db", os.ErrNotExist},
		{"Path is directory", "tests_data", PATH_IS_DIRECTORY},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("Testing: \"%s\" for path \"%s\"\n", tt.name, tt.path)
			err := ReadFileContent(tt.path)
			if !errors.Is(err, tt.expected) {
				t.Fatalf("Test fails, expected \"%s\", received \"%s\"\n", tt.expected.Error(), err.Error())
			}
		})
	}
}

func TestGetContent(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		expected string
	}{
		{"Test with keyword", "Some Text Test", ""},
	}
	InternalStore = make(map[string]int)   // initiating store
	StoreChannel = make(chan Action, 1024) // initiating the communication channel
	go StoreManager()                      // starting store manager as coroutine
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queryParams := url.Values{}
			queryParams.Add("text", tt.args)
			req, err := http.NewRequest("GET", "/", strings.NewReader(queryParams.Encode()))
			if err != nil {
				t.Fatalf("Error creating new request: %s", err.Error())
			}
			req.URL.RawQuery = queryParams.Encode()
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(GetContent)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != http.StatusOK {
				t.Fatalf("Handler return wrong status code: %v want %v", status, http.StatusOK)
			}
			resp := struct {
				Errors   []string
				Response Message
			}{}
			err = json.Unmarshal(rr.Body.Bytes(), &resp)
			if err != nil {
				t.Fatalf("Error unmarsheling response: %s", err.Error())
			}
			if len(resp.Errors) > 0 {
				t.Fatalf("Errors received %v", resp.Errors)
			}
			fmt.Printf("response: %v", resp.Response)
			argKeys := strings.Split(tt.args, " ")
			msg := resp.Response
			for _, key := range argKeys {
				if _, ok := msg.Counts[key]; !ok {
					t.Fatalf("Key %s not found in response count", key)
				}
			}
		})
	}
}

func BenchmarkGetContent(b *testing.B) {
	tests := []struct {
		name string
		args string
	}{
		{"Test with keyword", "Some Text Test"},
		{"Test with keyword", "Some Other Test"},
		{"Test with keyword", "Some Else Test"},
	}

	InternalStore = make(map[string]int)   // initiating store
	StoreChannel = make(chan Action, 1024) // initiating the communication channel
	go StoreManager()

	for _, tt := range tests {
		for n := 0; n < 100; n++ {
			queryParams := url.Values{}
			queryParams.Add("text", tt.args)
			req, err := http.NewRequest("GET", "/", strings.NewReader(queryParams.Encode()))
			if err != nil {
				b.Fatalf("Error creating new request: %s", err.Error())
			}
			req.URL.RawQuery = queryParams.Encode()
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(GetContent)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != http.StatusOK {
				b.Fatalf("Handler return wrong status code: %v want %v", status, http.StatusOK)
			}
			resp := struct {
				Errors   []string
				Response Message
			}{}
			err = json.Unmarshal(rr.Body.Bytes(), &resp)
			if err != nil {
				b.Fatalf("Error unmarsheling response: %s", err.Error())
			}
			if len(resp.Errors) > 0 {
				b.Fatalf("Errors received %v", resp.Errors)
			}
			argKeys := strings.Split(tt.args, " ")
			msg := resp.Response
			for _, key := range argKeys {
				if _, ok := msg.Counts[key]; !ok {
					b.Fatalf("Key %s not found in response count", key)
				}
			}
		}
	}
}

func BenchmarkPostContent(b *testing.B) {
	tests := []struct {
		name string
		args string
	}{
		{"Test with keyword", "Some Text Test"},
		{"Test with keyword", "Some Other Test"},
		{"Test with keyword", "Some Else Test"},
	}

	InternalStore = make(map[string]int)   // initiating store
	StoreChannel = make(chan Action, 1024) // initiating the communication channel
	go StoreManager()

	for _, tt := range tests {
		for n := 0; n < 100; n++ {
			queryParams := url.Values{}
			queryParams.Add("text", tt.args)
			req, err := http.NewRequest("POST", "/", strings.NewReader(queryParams.Encode()))
			if err != nil {
				b.Fatalf("Error creating new request: %s", err.Error())
			}
			req.URL.RawQuery = queryParams.Encode()
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(GetContent)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != http.StatusOK {
				b.Fatalf("Handler return wrong status code: %v want %v", status, http.StatusOK)
			}
			resp := struct {
				Errors   []string
				Response Message
			}{}
			err = json.Unmarshal(rr.Body.Bytes(), &resp)
			if err != nil {
				b.Fatalf("Error unmarsheling response: %s", err.Error())
			}
			if len(resp.Errors) > 0 {
				b.Fatalf("Errors received %v", resp.Errors)
			}
			argKeys := strings.Split(tt.args, " ")
			msg := resp.Response
			for _, key := range argKeys {
				if _, ok := msg.Counts[key]; !ok {
					b.Fatalf("Key %s not found in response count", key)
				}
			}
		}
	}
}
