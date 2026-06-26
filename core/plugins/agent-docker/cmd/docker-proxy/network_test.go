package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newProxyWithUpstream(t *testing.T, handler http.Handler, cfg *ProxyConfig) *DockerProxy {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)

	u, _ := url.Parse(upstream.URL)
	proxy, err := NewDockerProxy(cfg)
	require.NoError(t, err)

	proxy.upstream = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = u.Scheme
			req.URL.Host = u.Host
		},
	}
	return proxy
}

func TestEnsureSandboxNetwork_AlreadyExists(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/networks/") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"Name": "test_sandbox"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "test_sandbox",
		NetworkSubnet: "172.30.0.0/24",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.EnsureSandboxNetwork()
	assert.NoError(t, err)
}

func TestEnsureSandboxNetwork_CreateWithSubnet(t *testing.T) {
	var createdBody map[string]any

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/networks/") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/networks/create" {
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"Id": "net123"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "test_sandbox",
		NetworkSubnet: "172.30.0.0/24",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.EnsureSandboxNetwork()
	assert.NoError(t, err)
	assert.Equal(t, "test_sandbox", createdBody["Name"])
	assert.Equal(t, true, createdBody["Internal"])
	assert.Equal(t, "bridge", createdBody["Driver"])

	// Verify subnet was passed
	ipam, ok := createdBody["IPAM"].(map[string]any)
	require.True(t, ok)
	configs, ok := ipam["Config"].([]any)
	require.True(t, ok)
	require.Len(t, configs, 1)
	cfg := configs[0].(map[string]any)
	assert.Equal(t, "172.30.0.0/24", cfg["Subnet"])
}

func TestEnsureSandboxNetwork_SubnetOverlap_FallbackSucceeds(t *testing.T) {
	createAttempts := 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/networks/") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/networks/create" {
			createAttempts++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)

			if createAttempts == 1 {
				// First attempt with subnet — reject with overlap error
				assert.NotNil(t, body["IPAM"], "first attempt should include IPAM/subnet")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"message": "invalid pool request: Pool overlaps with other one on this address space",
				})
				return
			}
			// Second attempt without subnet — succeed
			assert.Nil(t, body["IPAM"], "fallback attempt should not include IPAM")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"Id": "net456"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "test_sandbox",
		NetworkSubnet: "172.30.0.0/24",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.EnsureSandboxNetwork()
	assert.NoError(t, err)
	assert.Equal(t, 2, createAttempts)
}

func TestEnsureSandboxNetwork_BothCreatesFail(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/networks/") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/networks/create" {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"message": "daemon error"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "test_sandbox",
		NetworkSubnet: "172.30.0.0/24",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.EnsureSandboxNetwork()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "daemon error")
}

func TestEnsureSandboxNetwork_NoSubnetConfigured(t *testing.T) {
	var createdBody map[string]any

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/networks/") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/networks/create" {
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"Id": "net789"})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "test_sandbox",
		NetworkSubnet: "", // no subnet configured
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.EnsureSandboxNetwork()
	assert.NoError(t, err)
	assert.Equal(t, "test_sandbox", createdBody["Name"])
	assert.Nil(t, createdBody["IPAM"], "should not include IPAM when no subnet configured")
}
