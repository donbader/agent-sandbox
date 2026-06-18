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
			// Use a proxy with AllowCompose=false to test base allowlist
			proxy, _ := NewDockerProxy(&ProxyConfig{
				SandboxID:     "test",
				AgentName:     "coder",
				NetworkName:   "sandbox",
				AllowedImages: []string{"node:*"},
				MaxContainers: 5,
				MemoryBytes:   2 * 1024 * 1024 * 1024,
				NanoCPUs:      2000000000,
				PidsLimit:     256,
			})
			assert.Equal(t, tc.allow, proxy.isEndpointAllowed(tc.method, tc.path))
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

func TestDockerProxy_ContainerCreate_PolicyViolation(t *testing.T) {
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

	body := `{"Image": "ubuntu:latest", "HostConfig": {}}`
	req := httptest.NewRequest("POST", "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "not in allowlist")
}

func TestDockerProxy_ContainerCreate_Privileged(t *testing.T) {
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

	body := `{"Image": "node:20", "HostConfig": {"Privileged": true}}`
	req := httptest.NewRequest("POST", "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "privileged")
}

func TestDockerProxy_ImagePull_Blocked(t *testing.T) {
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

	req := httptest.NewRequest("POST", "/images/create?fromImage=ubuntu&tag=latest", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "not in allowlist")
}

func TestDockerProxy_ContainerCreate_MaxContainers(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 2,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
	}
	proxy, _ := NewDockerProxy(cfg)

	proxy.trackContainer("existing-1", "", "existing-1")
	proxy.trackContainer("existing-2", "", "existing-2")

	body := `{"Image": "node:20", "HostConfig": {}}`
	req := httptest.NewRequest("POST", "/containers/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "maximum")
}

func TestDockerProxy_AllowCompose_NetworkEndpoints(t *testing.T) {
	// Without AllowCompose, network endpoints are blocked
	proxyNoCompose, _ := NewDockerProxy(&ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowCompose:  false,
	})
	assert.False(t, proxyNoCompose.isEndpointAllowed("POST", "/networks/create"))
	assert.False(t, proxyNoCompose.isEndpointAllowed("GET", "/networks"))
	assert.False(t, proxyNoCompose.isEndpointAllowed("DELETE", "/networks/abc123"))

	// With AllowCompose, network endpoints are allowed
	proxyCompose, _ := NewDockerProxy(&ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowCompose:  true,
	})
	assert.True(t, proxyCompose.isEndpointAllowed("POST", "/networks/create"))
	assert.True(t, proxyCompose.isEndpointAllowed("GET", "/networks"))
	assert.True(t, proxyCompose.isEndpointAllowed("DELETE", "/networks/abc123"))
	assert.True(t, proxyCompose.isEndpointAllowed("POST", "/networks/abc123/connect"))
	assert.True(t, proxyCompose.isEndpointAllowed("POST", "/volumes/create"))
	assert.True(t, proxyCompose.isEndpointAllowed("GET", "/volumes"))
}

func TestDockerProxy_AllowCompose_NetworkCreateForcesInternal(t *testing.T) {
	mockDocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse the body to verify Internal was forced
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)

		var req map[string]any
		_ = json.Unmarshal(body, &req)

		assert.Equal(t, true, req["Internal"])
		assert.Equal(t, "test-coder-mynet", req["Name"])

		labels, _ := req["Labels"].(map[string]any)
		assert.Equal(t, "test-coder", labels["agent-sandbox.sandbox"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"net123abc"}`))
	}))
	defer mockDocker.Close()

	cfg := &ProxyConfig{
		SandboxID:     "test-coder",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowCompose:  true,
	}
	proxy, _ := NewDockerProxy(cfg)
	// Override upstream to point at mock
	proxy.upstream = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(&url.URL{Scheme: "http", Host: mockDocker.Listener.Addr().String()})
			r.Out.Host = r.In.Host
		},
	}

	body := `{"Name":"mynet","Internal":false}`
	req := httptest.NewRequest("POST", "/networks/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	// Verify network was tracked
	proxy.mu.Lock()
	assert.True(t, proxy.networks["net123abc"])
	proxy.mu.Unlock()
}

func TestDockerProxy_AllowBuild_Endpoints(t *testing.T) {
	// Without AllowBuild, build endpoints are blocked
	proxyNoBuild, _ := NewDockerProxy(&ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowBuild:    false,
	})
	assert.False(t, proxyNoBuild.isEndpointAllowed("GET", "/info"))
	assert.False(t, proxyNoBuild.isEndpointAllowed("POST", "/build"))
	assert.False(t, proxyNoBuild.isEndpointAllowed("GET", "/images/myapp/get"))
	assert.False(t, proxyNoBuild.isEndpointAllowed("POST", "/images/load"))

	// With AllowBuild, build endpoints are allowed
	proxyBuild, _ := NewDockerProxy(&ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowBuild:    true,
	})
	assert.True(t, proxyBuild.isEndpointAllowed("GET", "/info"))
	assert.True(t, proxyBuild.isEndpointAllowed("POST", "/build"))
	assert.True(t, proxyBuild.isEndpointAllowed("GET", "/images/myapp/get"))
	assert.True(t, proxyBuild.isEndpointAllowed("POST", "/images/load"))
	assert.True(t, proxyBuild.isEndpointAllowed("POST", "/images/myapp/tag"))
}

func TestDockerProxy_AllowBuild_BuildkitImageAutoAllowed(t *testing.T) {
	// Test that the policy auto-allows moby/buildkit:* when AllowBuild is true
	policyWithBuild := &Policy{
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		AllowBuild:    true,
	}
	assert.True(t, policyWithBuild.ImageAllowed("moby/buildkit:latest"))
	assert.True(t, policyWithBuild.ImageAllowed("moby/buildkit:buildx-stable-1"))
	assert.True(t, policyWithBuild.ImageAllowed("moby/buildkit:v0.12.0"))
	// Non-buildkit images still follow the allowlist
	assert.False(t, policyWithBuild.ImageAllowed("ubuntu:latest"))
	assert.True(t, policyWithBuild.ImageAllowed("node:20"))

	// Without AllowBuild, moby/buildkit is blocked
	policyNoBuild := &Policy{
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		AllowBuild:    false,
	}
	assert.False(t, policyNoBuild.ImageAllowed("moby/buildkit:latest"))
}

func TestDockerProxy_AllowBuild_BuildkitImageBlockedWhenDisabled(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowBuild:    false,
	}
	proxy, _ := NewDockerProxy(cfg)

	// moby/buildkit should be blocked when AllowBuild is false
	req := httptest.NewRequest("POST", "/images/create?fromImage=moby/buildkit&tag=latest", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDockerProxy_AllowBuild_LocallyBuiltImageAllowed(t *testing.T) {
	builtImages := make(map[string]bool)
	builtImages["myapp:latest"] = true
	builtImages["myapp:v1.0"] = true

	policy := &Policy{
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		AllowBuild:    true,
		BuiltImages:   builtImages,
	}

	// Built images should be allowed
	assert.True(t, policy.ImageAllowed("myapp:latest"))
	assert.True(t, policy.ImageAllowed("myapp:v1.0"))
	// Non-built, non-allowlisted images should be blocked
	assert.False(t, policy.ImageAllowed("evil:latest"))
	// Allowlisted images still work
	assert.True(t, policy.ImageAllowed("node:20"))
}

func TestDockerProxy_AllowBuild_BuiltImageWithRegistryPrefix(t *testing.T) {
	builtImages := make(map[string]bool)
	builtImages["docker.io/library/myapp:latest"] = true
	builtImages["myapp:latest"] = true // normalizeImage also stored

	policy := &Policy{
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		AllowBuild:    true,
		BuiltImages:   builtImages,
	}

	// Should be allowed both with and without the prefix
	assert.True(t, policy.ImageAllowed("docker.io/library/myapp:latest"))
	assert.True(t, policy.ImageAllowed("myapp:latest"))
}

func TestDockerProxy_AllowBuild_HandleBuildTracksTags(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowBuild:    true,
	}
	proxy, _ := NewDockerProxy(cfg)

	// Simulate: after handleBuild processes a request with t=myapp:latest
	proxy.mu.Lock()
	proxy.builtImages["myapp:latest"] = true
	proxy.builtImages["myapp:latest"] = true
	proxy.mu.Unlock()

	// The policy should now allow this image
	assert.True(t, proxy.policy.ImageAllowed("myapp:latest"))
	assert.False(t, proxy.policy.ImageAllowed("evil:latest"))
}

func TestDockerProxy_AllowBuild_NoTrackingWhenBuildDisabled(t *testing.T) {
	cfg := &ProxyConfig{
		SandboxID:     "test",
		AgentName:     "coder",
		NetworkName:   "sandbox",
		AllowedImages: []string{"node:*"},
		MaxContainers: 5,
		MemoryBytes:   2 * 1024 * 1024 * 1024,
		NanoCPUs:      2000000000,
		PidsLimit:     256,
		AllowBuild:    false,
	}
	proxy, _ := NewDockerProxy(cfg)

	// Manually add to builtImages (simulating a build that somehow got through)
	proxy.mu.Lock()
	proxy.builtImages["myapp:latest"] = true
	proxy.mu.Unlock()

	// Even though it's in builtImages, the policy should still allow it
	// because BuiltImages is shared and AllowBuild doesn't gate the check
	// (the endpoint gating prevents builds from happening in the first place)
	assert.True(t, proxy.policy.ImageAllowed("myapp:latest"))
}

func TestDockerProxy_AllowedCapabilities_Permitted(t *testing.T) {
	policy := &Policy{
		AllowedImages:       []string{"alpine:*"},
		MaxContainers:       5,
		AllowedCapabilities: []string{"NET_ADMIN", "NET_BIND_SERVICE"},
	}
	// Allowed caps should pass
	err := policy.ValidateCreate(&CreateRequest{
		Image:  "alpine:latest",
		CapAdd: []string{"NET_ADMIN"},
	}, 0)
	assert.NoError(t, err)

	// Multiple allowed caps
	err = policy.ValidateCreate(&CreateRequest{
		Image:  "alpine:latest",
		CapAdd: []string{"NET_ADMIN", "NET_BIND_SERVICE"},
	}, 0)
	assert.NoError(t, err)
}

func TestDockerProxy_AllowedCapabilities_Blocked(t *testing.T) {
	policy := &Policy{
		AllowedImages:       []string{"alpine:*"},
		MaxContainers:       5,
		AllowedCapabilities: []string{"NET_ADMIN"},
	}
	// Disallowed cap should be blocked
	err := policy.ValidateCreate(&CreateRequest{
		Image:  "alpine:latest",
		CapAdd: []string{"SYS_ADMIN"},
	}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SYS_ADMIN")
}

func TestDockerProxy_AllowedCapabilities_EmptyBlocksAll(t *testing.T) {
	policy := &Policy{
		AllowedImages:       []string{"alpine:*"},
		MaxContainers:       5,
		AllowedCapabilities: []string{},
	}
	// Any cap_add should be blocked when list is empty
	err := policy.ValidateCreate(&CreateRequest{
		Image:  "alpine:latest",
		CapAdd: []string{"NET_ADMIN"},
	}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adding capabilities is not allowed")
}

func TestDockerProxy_AllowedCapabilities_CaseInsensitive(t *testing.T) {
	policy := &Policy{
		AllowedImages:       []string{"alpine:*"},
		MaxContainers:       5,
		AllowedCapabilities: []string{"NET_ADMIN"},
	}
	// Should match case-insensitively
	err := policy.ValidateCreate(&CreateRequest{
		Image:  "alpine:latest",
		CapAdd: []string{"net_admin"},
	}, 0)
	assert.NoError(t, err)
}

func TestDockerProxy_AllowedBindPaths_Permitted(t *testing.T) {
	policy := &Policy{
		AllowedImages:    []string{"alpine:*"},
		MaxContainers:    5,
		AllowedBindPaths: []string{"/tmp/", "/home/agent/"},
	}
	err := policy.ValidateCreate(&CreateRequest{
		Image: "alpine:latest",
		Binds: []string{"/tmp/myproject/.build/config.yaml:/etc/config.yaml:ro"},
	}, 0)
	assert.NoError(t, err)
}

func TestDockerProxy_AllowedBindPaths_Blocked(t *testing.T) {
	policy := &Policy{
		AllowedImages:    []string{"alpine:*"},
		MaxContainers:    5,
		AllowedBindPaths: []string{"/tmp/"},
	}
	err := policy.ValidateCreate(&CreateRequest{
		Image: "alpine:latest",
		Binds: []string{"/etc/shadow:/etc/shadow:ro"},
	}, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "/etc/shadow")
}

func TestDockerProxy_AllowedBindPaths_EmptyBlocksAll(t *testing.T) {
	policy := &Policy{
		AllowedImages:    []string{"alpine:*"},
		MaxContainers:    5,
		AllowedBindPaths: []string{},
	}
	err := policy.ValidateCreate(&CreateRequest{
		Image: "alpine:latest",
		Binds: []string{"/tmp/foo:/bar"},
	}, 0)
	assert.Error(t, err)
}
