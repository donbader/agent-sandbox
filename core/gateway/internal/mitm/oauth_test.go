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

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAuthMiddleware_InjectsBearer(t *testing.T) {
	gateway.ResetForTesting()
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "test-access-token",
		RefreshToken:  strPtr("test-refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "client-id",
		ClientSecret:  nil,
	})

	err := RegisterOAuthMiddleware("test-oauth", []string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "https://mcp.notion.com/mcp", nil)
	req.Host = "mcp.notion.com"

	matching := gateway.MatchingMiddleware(req)
	require.NotEmpty(t, matching)

	ctx := &gateway.MiddlewareContext{Request: req, Env: os.Getenv}
	err = matching[0].Func(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-access-token", req.Header.Get("Authorization"))
}

func TestOAuthMiddleware_SkipsNonMatchingDomain(t *testing.T) {
	gateway.ResetForTesting()
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "token",
		RefreshToken:  strPtr("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	err := RegisterOAuthMiddleware("test-oauth", []string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "https://api.github.com/repos", nil)
	req.Host = "api.github.com"

	matching := gateway.MatchingMiddleware(req)
	assert.Empty(t, matching)
}

func TestOAuthMiddleware_RefreshesExpiredToken(t *testing.T) {
	gateway.ResetForTesting()

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
		RefreshToken:  strPtr("old-refresh"),
		ExpiresAt:     time.Now().Unix() - 100,
		TokenEndpoint: server.URL,
		ClientID:      "client-id",
		ClientSecret:  nil,
	})

	state := &oauthState{
		tokenFile:  tokenFile,
		httpClient: server.Client(),
	}

	token, err := state.getValidToken()
	require.NoError(t, err)
	assert.Equal(t, "new-access-token", token)

	// Verify token file was updated.
	data, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	var saved StoredToken
	require.NoError(t, json.Unmarshal(data, &saved))
	assert.Equal(t, "new-access-token", saved.AccessToken)
	assert.Equal(t, "new-refresh", *saved.RefreshToken)
}

func TestOAuthMiddleware_RejectsHTTPTokenEndpoint(t *testing.T) {
	gateway.ResetForTesting()
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "expired-token",
		RefreshToken:  strPtr("refresh"),
		ExpiresAt:     time.Now().Unix() - 100,
		TokenEndpoint: "http://evil.example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	state := &oauthState{
		tokenFile:  tokenFile,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	_, err := state.getValidToken()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must use https")
}

func TestOAuthMiddleware_BlocksPrivateIPEndpoint(t *testing.T) {
	gateway.ResetForTesting()
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "expired-token",
		RefreshToken:  strPtr("refresh"),
		ExpiresAt:     time.Now().Unix() - 100,
		TokenEndpoint: "https://127.0.0.1:9999/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	state := &oauthState{
		tokenFile:  tokenFile,
		httpClient: &http.Client{Transport: ssrfSafeTransport(), Timeout: 5 * time.Second},
	}

	_, err := state.getValidToken()
	assert.Error(t, err)
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

func TestRegisterOAuthMiddleware_ErrorsWithoutTokenFile(t *testing.T) {
	gateway.ResetForTesting()
	err := RegisterOAuthMiddleware("test", []string{"mcp.example.com"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token_file is required")
}

func TestOAuthMiddleware_HandlesHostWithPort(t *testing.T) {
	gateway.ResetForTesting()
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "port-token",
		RefreshToken:  strPtr("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	err := RegisterOAuthMiddleware("test-oauth", []string{"mcp.notion.com"}, tokenFile)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "https://mcp.notion.com:443/mcp", nil)
	req.Host = "mcp.notion.com:443"

	matching := gateway.MatchingMiddleware(req)
	require.NotEmpty(t, matching)

	ctx := &gateway.MiddlewareContext{Request: req, Env: os.Getenv}
	err = matching[0].Func(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Bearer port-token", req.Header.Get("Authorization"))
}

func TestOAuthMiddleware_CachesToken(t *testing.T) {
	gateway.ResetForTesting()
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "cached-token",
		RefreshToken:  strPtr("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	state := &oauthState{
		tokenFile:  tokenFile,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}

	// First call reads from file.
	token1, err := state.getValidToken()
	require.NoError(t, err)
	assert.Equal(t, "cached-token", token1)

	// Delete the token file — second call should use cache.
	_ = os.Remove(tokenFile)

	token2, err := state.getValidToken()
	require.NoError(t, err)
	assert.Equal(t, "cached-token", token2)
}

func TestOAuthSecrets(t *testing.T) {
	tokenFile := writeTestToken(t, &StoredToken{
		AccessToken:   "super-secret-token",
		RefreshToken:  strPtr("refresh"),
		ExpiresAt:     time.Now().Unix() + 3600,
		TokenEndpoint: "https://example.com/token",
		ClientID:      "cid",
		ClientSecret:  nil,
	})

	secrets := OAuthSecrets(tokenFile)
	assert.Contains(t, secrets, "super-secret-token")
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

func strPtr(s string) *string {
	return &s
}
