package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
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

func TestDiscoverSandboxNetwork_Success(t *testing.T) {
	hostname, _ := os.Hostname()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == fmt.Sprintf("/containers/%s/json", hostname) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"my-project_sandbox": map[string]any{
							"NetworkID": "abc123def456",
						},
					},
				},
			})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "my-project-coder",
		AgentName:     "coder",
		NetworkName:   "my-project_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.NoError(t, err)
	assert.Equal(t, "abc123def456", proxy.cfg.NetworkID)
}

func TestDiscoverSandboxNetwork_NetworkNotOnContainer_FallbackBySuffix(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{
					"chome-matv3-ihbums_sandbox": map[string]any{
						"NetworkID": "real-net-id-999",
					},
					"bridge": map[string]any{
						"NetworkID": "bridge0",
					},
				},
			},
		})
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "my-agent-team-v3_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.NoError(t, err)
	assert.Equal(t, "real-net-id-999", proxy.cfg.NetworkID)
}

func TestDiscoverSandboxNetwork_NetworkNotOnContainer_NoFallback(t *testing.T) {
	hostname, _ := os.Hostname()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == fmt.Sprintf("/containers/%s/json", hostname) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"some_other_network": map[string]any{
							"NetworkID": "other999",
						},
					},
				},
			})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "my-project-coder",
		AgentName:     "coder",
		NetworkName:   "my-project_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox network \"my-project_sandbox\" not found")
	assert.Contains(t, err.Error(), "some_other_network")
}

func TestDiscoverSandboxNetwork_InspectFails(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "no such container"})
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "my-project-coder",
		AgentName:     "coder",
		NetworkName:   "my-project_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestDiscoverSandboxNetwork_MultipleNetworks(t *testing.T) {
	hostname, _ := os.Hostname()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == fmt.Sprintf("/containers/%s/json", hostname) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"bridge": map[string]any{
							"NetworkID": "bridgenet111",
						},
						"my-project_sandbox": map[string]any{
							"NetworkID": "sandboxnet222",
						},
					},
				},
			})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "my-project-coder",
		AgentName:     "coder",
		NetworkName:   "my-project_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.NoError(t, err)
	// Should pick the correct network by name, not the bridge
	assert.Equal(t, "sandboxnet222", proxy.cfg.NetworkID)
}
