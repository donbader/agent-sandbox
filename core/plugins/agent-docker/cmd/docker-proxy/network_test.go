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

func TestDiscoverSandboxNetwork_SingleNonDefault(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/containers/") {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"my-project_sandbox": map[string]any{
							"NetworkID": "abc123def456",
						},
						"bridge": map[string]any{
							"NetworkID": "bridge0000",
						},
					},
				},
			})
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
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

func TestDiscoverSandboxNetwork_NameMismatch_StillWorks(t *testing.T) {
	// Compose project name differs from SANDBOX_NETWORK — proxy finds it anyway
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

func TestDiscoverSandboxNetwork_MultipleNonDefault_ExactMatch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{
					"my-project_sandbox": map[string]any{
						"NetworkID": "sandbox123",
					},
					"my-project_default": map[string]any{
						"NetworkID": "default456",
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
		NetworkName:   "my-project_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.NoError(t, err)
	assert.Equal(t, "sandbox123", proxy.cfg.NetworkID)
}

func TestDiscoverSandboxNetwork_MultipleNonDefault_NoExactMatch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{
					"unknown_net_a": map[string]any{
						"NetworkID": "aaa",
					},
					"unknown_net_b": map[string]any{
						"NetworkID": "bbb",
					},
				},
			},
		})
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
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
	assert.Contains(t, err.Error(), "multiple non-default networks")
}

func TestDiscoverSandboxNetwork_OnlyDefaultNetworks(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"NetworkSettings": map[string]any{
				"Networks": map[string]any{
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
		NetworkName:   "my-project_sandbox",
		AllowedImages: []string{"*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	})

	err := proxy.DiscoverSandboxNetwork()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sandbox network found")
}

func TestDiscoverSandboxNetwork_ContainerNotFound(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "No such container"})
	})

	proxy := newProxyWithUpstream(t, handler, &ProxyConfig{
		SandboxID:     "test",
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

func TestIsDefaultNetwork(t *testing.T) {
	assert.True(t, isDefaultNetwork("bridge"))
	assert.True(t, isDefaultNetwork("host"))
	assert.True(t, isDefaultNetwork("none"))
	assert.False(t, isDefaultNetwork("my-project_sandbox"))
	assert.False(t, isDefaultNetwork("chome-matv3-ihbums_sandbox"))
	assert.False(t, isDefaultNetwork("custom_network"))
}
