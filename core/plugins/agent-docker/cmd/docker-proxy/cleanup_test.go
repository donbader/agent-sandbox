package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanup_ListsAndRemovesContainers(t *testing.T) {
	var calls []string

	mockDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == "GET" && r.URL.Path == "/containers/json":
			containers := []map[string]any{
				{"Id": "abc123full", "Names": []string{"/test-coder-app1"}},
				{"Id": "def456full", "Names": []string{"/test-coder-app2"}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(containers)
		case r.Method == "POST" && (r.URL.Path == "/containers/abc123full/stop" || r.URL.Path == "/containers/def456full/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "DELETE" && (r.URL.Path == "/containers/abc123full" || r.URL.Path == "/containers/def456full"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockDocker.Close()

	cleaner := &Cleaner{
		sandboxID:  "test",
		dockerAddr: mockDocker.URL,
	}

	cleaner.CleanupAll(context.Background())

	assert.Contains(t, calls, "GET /containers/json")
	assert.Contains(t, calls, "POST /containers/abc123full/stop")
	assert.Contains(t, calls, "POST /containers/def456full/stop")
	assert.Contains(t, calls, "DELETE /containers/abc123full")
	assert.Contains(t, calls, "DELETE /containers/def456full")
}
