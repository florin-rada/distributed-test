package main

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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InternalStore = make(map[string]int)   // initiating store
			StoreChannel = make(chan Action, 1024) // initiating the communication channel
			go StoreManager()                      // starting store manager as coroutine
			queryParams := url.Values{}
			queryParams.Add("text", tt.args)
			fmt.Printf("Encoded params: %s", queryParams.Encode())
			req, err := http.NewRequest("GET", "/", strings.NewReader(queryParams.Encode()))
			if err != nil {
				t.Fatalf("Error creating new request: %s", err.Error())
			}
			fmt.Printf("raw query: %s", req.URL.RawQuery)
			req.URL.RawQuery = queryParams.Encode()
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(GetContent)
			handler.ServeHTTP(rr, req)
			if status := rr.Code; status != http.StatusOK {
				t.Fatalf("Handler return wrong status code: %v want %v", status, http.StatusOK)
			}
			resp := Response{}
			err = json.Unmarshal(rr.Body.Bytes(), &resp)
			if err != nil {
				t.Fatalf("Error unmarsheling response: %s", err.Error())
			}
			if len(resp.Errors) > 0 {
				t.Fatalf("Errors received %v", resp.Errors)
			}
			fmt.Printf("response: %v", resp.Response)
			argKeys := strings.Split(tt.args, " ")
			msg := resp.Response.(Message)
			for _, key := range argKeys {
				if _, ok := msg.Counts[key]; !ok {
					t.Fatalf("Key %s not found in response count", key)
				}
			}
		})
	}
}
