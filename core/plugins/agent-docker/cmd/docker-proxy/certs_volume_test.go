package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCertsArchive_OnlyAllowListedFiles(t *testing.T) {
	// Create a temp directory with mixed files (allowed + forbidden)
	dir := t.TempDir()

	// Override certsDir for this test (requires package-level var or test helper)
	// Since certsDir is a const, we test via the allowlist behavior instead
	t.Run("allowlist excludes private keys", func(t *testing.T) {
		assert.True(t, certsAllowList["gateway-route.sh"])
		assert.True(t, certsAllowList["ca.crt"])
		assert.False(t, certsAllowList["ca.key"])
		assert.False(t, certsAllowList["private.pem"])
		assert.False(t, certsAllowList[""])
	})

	// If /shared/certs exists, test actual archive building
	if _, err := os.Stat(certsDir); err == nil {
		t.Run("archive contains only allowed files", func(t *testing.T) {
			archive, err := buildCertsArchive()
			require.NoError(t, err)
			assert.NotEmpty(t, archive)

			// Verify no .key files in the archive
			// (tar header names are visible as substrings in the raw bytes)
			assert.NotContains(t, string(archive), "ca.key")
			assert.Contains(t, string(archive), "gateway-route.sh")
			assert.Contains(t, string(archive), "ca.crt")
		})
	}

	_ = dir // suppress unused
	_ = filepath.Join(dir, "test") // suppress unused import
}

func TestExtractNetworkIDFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/networks/abc123/connect", "abc123"},
		{"/v1.45/networks/abc123/connect", "abc123"},
		{"/networks/my-network/disconnect", "my-network"},
		{"/v1.43/networks/net-id-here/connect", "net-id-here"},
		{"/containers/abc/json", ""},
		{"/volumes/vol1", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := extractNetworkIDFromPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandleBuild_OnlyTracksOnSuccess(t *testing.T) {
	// Backend that responds to build (200 OK streaming) and image inspect
	imageExists := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/build":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stream":"Step 1/1"}`))
		case r.Method == "GET" && r.URL.Path == "/images/myapp:latest/json":
			if imageExists {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"Id":"sha256:abc"}`))
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	proxy.cfg.AllowBuild = true

	// Build with image NOT existing after → should NOT track
	req := httptest.NewRequest("POST", "/build?t=myapp:latest", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, proxy.builtImages["myapp:latest"], "should not track failed build")

	// Now make image exist and rebuild
	imageExists = true
	req = httptest.NewRequest("POST", "/build?t=myapp:latest", nil)
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, proxy.builtImages["myapp:latest"], "should track successful build")
}

func TestHandleImageTag_OnlyTracksOn201(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/src:v1/tag" && r.Method == "POST" {
			// Simulate success
			w.WriteHeader(http.StatusCreated)
		} else if r.URL.Path == "/images/nonexistent/tag" && r.Method == "POST" {
			// Simulate failure
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	proxy.cfg.AllowBuild = true

	// Failed tag → should NOT track
	req := httptest.NewRequest("POST", "/images/nonexistent/tag?repo=myapp&tag=latest", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.False(t, proxy.builtImages["myapp:latest"], "should not track failed tag")

	// Successful tag → should track
	req = httptest.NewRequest("POST", "/images/src:v1/tag?repo=myapp&tag=v2", nil)
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.True(t, proxy.builtImages["myapp:v2"], "should track successful tag")
}

func TestHandleNetworkConnect_VerifiesNetworkOwnership(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/containers/owned-ctr/json":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Id":"owned-ctr","Config":{"Labels":{"agent-sandbox.sandbox":"test"}}}`))
		case r.Method == "GET" && r.URL.Path == "/networks/foreign-net":
			// Network not owned by this sandbox
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Id":"foreign-net","Labels":{"agent-sandbox.sandbox":"other"}}`))
		case r.Method == "POST":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()

	proxy := newTestProxy(t, backend)
	proxy.cfg.AllowCompose = true
	proxy.cfg.NetworkID = "sandbox-net-id" // Our sandbox network

	// Connect owned container to foreign network → should be blocked
	body := `{"Container":"owned-ctr"}`
	req := httptest.NewRequest("POST", "/networks/foreign-net/connect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "not owned")

	// Connect owned container to sandbox network → should succeed
	body = `{"Container":"owned-ctr"}`
	req = httptest.NewRequest("POST", "/networks/sandbox-net-id/connect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
