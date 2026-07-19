package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockDockerBackend creates a test server that simulates Docker's /containers/json endpoint.
func mockDockerBackend(t *testing.T, containers []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(containers)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func newTestProxy(t *testing.T, backend *httptest.Server) *DockerProxy {
	t.Helper()
	backendURL, _ := url.Parse(backend.URL)
	proxy, _ := NewDockerProxy(&ProxyConfig{
		SandboxID:     "test-sandbox",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 10,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})
	proxy.upstream = httputil.NewSingleHostReverseProxy(backendURL)
	return proxy
}

func TestCountOwnedContainers_ReturnsCorrectCount(t *testing.T) {
	containers := []map[string]any{
		{"Id": "abc123", "Names": []string{"/test-container-1"}},
		{"Id": "def456", "Names": []string{"/test-container-2"}},
		{"Id": "ghi789", "Names": []string{"/test-container-3"}},
	}
	backend := mockDockerBackend(t, containers)
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	count := proxy.countOwnedContainers()
	assert.Equal(t, 3, count)
}

func TestCountOwnedContainers_EmptyList(t *testing.T) {
	backend := mockDockerBackend(t, []map[string]any{})
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	count := proxy.countOwnedContainers()
	assert.Equal(t, 0, count)
}

func TestCountOwnedContainers_ErrorResponse(t *testing.T) {
	// Backend returns 500
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	count := proxy.countOwnedContainers()
	// Fail-closed: returns MaxContainers on error (blocks create)
	assert.Equal(t, 10, count)
}

func TestCountOwnedContainers_MalformedJSON(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	count := proxy.countOwnedContainers()
	// Fail-closed: returns MaxContainers on parse error
	assert.Equal(t, 10, count)
}

func TestCountOwnedContainers_UsedInValidateCreate(t *testing.T) {
	// Simulate: 9 containers exist (below limit of 10)
	containers := make([]map[string]any, 9)
	for i := range containers {
		containers[i] = map[string]any{"Id": "container-" + string(rune('a'+i))}
	}
	backend := mockDockerBackend(t, containers)
	defer backend.Close()

	proxy := newTestProxy(t, backend)

	// Should allow create (9 < 10)
	err := proxy.policy.ValidateCreate(&CreateRequest{Image: "node:20"}, proxy.countOwnedContainers())
	assert.NoError(t, err)

	// Now add one more to simulate 10 containers (at limit)
	containers = append(containers, map[string]any{"Id": "container-j"})
	backend.Close()
	backend2 := mockDockerBackend(t, containers)
	defer backend2.Close()
	backendURL, _ := url.Parse(backend2.URL)
	proxy.upstream = httputil.NewSingleHostReverseProxy(backendURL)

	// Should block create (10 >= 10)
	err = proxy.policy.ValidateCreate(&CreateRequest{Image: "node:20"}, proxy.countOwnedContainers())
	assert.ErrorContains(t, err, "maximum container limit")
}
