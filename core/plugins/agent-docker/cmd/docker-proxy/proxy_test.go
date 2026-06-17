package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEndpointAllowed(t *testing.T) {
	cases := []struct {
		method string
		path   string
		allow  bool
	}{
		{"POST", "/containers/create", true},
		{"POST", "/containers/abc123/start", true},
		{"POST", "/containers/abc123/stop", true},
		{"POST", "/containers/abc123/kill", true},
		{"DELETE", "/containers/abc123", true},
		{"GET", "/containers/abc123/json", true},
		{"GET", "/containers/abc123/logs", true},
		{"GET", "/containers/json", true},
		{"POST", "/containers/abc123/exec", true},
		{"POST", "/exec/abc123/start", true},
		{"GET", "/images/json", true},
		{"POST", "/images/create", true},
		// Blocked
		{"GET", "/volumes", false},
		{"POST", "/volumes/create", false},
		{"GET", "/networks", false},
		{"GET", "/swarm", false},
		{"GET", "/secrets", false},
		{"GET", "/configs", false},
		{"GET", "/system/info", false},
		{"GET", "/unknown/endpoint", false},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			assert.Equal(t, tc.allow, isEndpointAllowed(tc.method, tc.path))
		})
	}
}

func TestDockerProxy_BlockedEndpoint(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	req := httptest.NewRequest("GET", "/volumes", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDockerProxy_VersionPrefixStripped(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	// Versioned path to a blocked endpoint should still be blocked
	req := httptest.NewRequest("GET", "/v1.43/volumes", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDockerProxy_UnownedContainer(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	// Try to start a container we don't own
	req := httptest.NewRequest("POST", "/containers/unknown123/start", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestExtractContainerID(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/containers/abc123/start", "abc123"},
		{"/containers/abc123/json", "abc123"},
		{"/containers/my-container/stop", "my-container"},
		{"/containers/json", ""},
		{"/containers/create", ""},
		{"/images/json", ""},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, extractContainerID(tc.path))
		})
	}
}
