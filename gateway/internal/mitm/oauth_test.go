package mitm

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthRewriter_InjectsBearer(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "test-access-token",
		RefreshToken:  new("test-refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "client-id",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req.Host = "mcp.notion.com"

	modified := rw.RewriteRequest(req)
	assert.True(t, modified)
	assert.Equal(t, "Bearer test-access-token", req.Header.Get("Authorization"))
}

func TestOAuthRewriter_SkipsNonMatchingDomain(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "token",
		RefreshToken:  new("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "https://api.github.com/repos", nil)
	req.Host = "api.github.com"

	modified := rw.RewriteRequest(req)
	assert.False(t, modified)
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestOAuthRewriter_RefreshesExpiredToken(t *testing.T) {
	// Mock token endpoint that returns a new token (TLS for HTTPS validation).
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		err := r.ParseForm()
		require.NoError(t, err)
		assert.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		assert.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		assert.Equal(t, "client-id", r.Form.Get("client_id"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "expired-token",
		RefreshToken:  new("old-refresh"),
		ExpiresAt:     time.Now().Unix() - 100, // Already expired.
		TokenEndpoint: server.URL,              // https://127.0.0.1:PORT
		ClientID:      "client-id",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)
	// Use TLS server's client (trusts test cert, bypasses SSRF dialer for localhost).
	rw.httpClient = server.Client()

	req := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req.Host = "mcp.notion.com"

	modified := rw.RewriteRequest(req)
	assert.True(t, modified)
	assert.Equal(t, "Bearer new-access-token", req.Header.Get("Authorization"))

	// Verify token file was updated.
	data, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	var saved StoredToken
	require.NoError(t, json.Unmarshal(data, &saved))
	assert.Equal(t, "new-access-token", saved.AccessToken)
	assert.Equal(t, "new-refresh", *saved.RefreshToken)
}

func TestOAuthRewriter_RejectsHTTPTokenEndpoint(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "expired-token",
		RefreshToken:  new("refresh"),
		ExpiresAt:     time.Now().Unix() - 100, // Expired — triggers refresh.
		TokenEndpoint: "http://evil.example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req.Host = "mcp.notion.com"

	// Should fail because token_endpoint is http, not https.
	modified := rw.RewriteRequest(req)
	assert.False(t, modified)
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestOAuthRewriter_BlocksPrivateIPEndpoint(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "expired-token",
		RefreshToken:  new("refresh"),
		ExpiresAt:     time.Now().Unix() - 100, // Expired — triggers refresh.
		TokenEndpoint: "https://127.0.0.1:9999/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)
	// Keep the default SSRF-safe transport (don't override httpClient).

	req := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req.Host = "mcp.notion.com"

	// Should fail because 127.0.0.1 is a private IP.
	modified := rw.RewriteRequest(req)
	assert.False(t, modified)
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestValidateTokenEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://auth.example.com/token", false},
		{"http rejected", "http://auth.example.com/token", true},
		{"empty scheme", "://auth.example.com/token", true},
		{"ftp rejected", "ftp://auth.example.com/token", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTokenEndpoint(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2607:f8b0:4004:800::200e", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip)
			assert.Equal(t, tt.private, isPrivateIP(ip))
		})
	}
}

func TestOAuthRewriter_ErrorsWithoutTokenFile(t *testing.T) {
	_, err := NewOAuthRewriter([]string{"mcp.example.com"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token_file is required")
}

func TestOAuthRewriter_HandlesHostWithPort(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "port-token",
		RefreshToken:  new("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "https://mcp.notion.com:443/mcp", nil)
	req.Host = "mcp.notion.com:443"

	modified := rw.RewriteRequest(req)
	assert.True(t, modified)
	assert.Equal(t, "Bearer port-token", req.Header.Get("Authorization"))
}

func TestOAuthRewriter_CachesToken(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "cached-token",
		RefreshToken:  new("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	rw, err := NewOAuthRewriter([]string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	// First request reads from file.
	req1 := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req1.Host = "mcp.notion.com"
	rw.RewriteRequest(req1)

	// Delete the token file — second request should use cache.
	_ = os.Remove(tokenFile)

	req2 := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req2.Host = "mcp.notion.com"
	modified := rw.RewriteRequest(req2)
	assert.True(t, modified)
	assert.Equal(t, "Bearer cached-token", req2.Header.Get("Authorization"))
}

// --- helpers ---

func writeTestToken(t *testing.T, token *StoredToken) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	data, err := json.Marshal(token)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

//go:fix inline
func strPtr(s string) *string {
	return new(s)
}
