package custom

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

type oauthCallbackConfig struct {
	TokenEndpoint string
	ClientID      string
	ClientSecret  string
}

var (
	oauthCallbackProviders = map[string]oauthCallbackConfig{}
	oauthCallbackTokenDir  string
	oauthCallbackPublicURL string
	oauthNonces            = map[string]string{}
	oauthNoncesMu          sync.Mutex
)

func init() {
	oauthCallbackTokenDir = "{{ .options.token_dir }}"
	oauthCallbackPublicURL = "{{ .public_url }}"
	providersJSON := `{{ toJSON .options.providers }}`
	var providers map[string]map[string]any
	if err := json.Unmarshal([]byte(providersJSON), &providers); err != nil {
		slog.Error("oauth-callback: failed to parse providers config", "error", err)
	} else {
		for name, cfg := range providers {
			p := oauthCallbackConfig{}
			if v, ok := cfg["token_endpoint"].(string); ok {
				p.TokenEndpoint = v
			}
			if v, ok := cfg["client_id"].(string); ok {
				p.ClientID = v
			}
			if v, ok := cfg["client_secret"].(string); ok {
				p.ClientSecret = v
			}
			oauthCallbackProviders[name] = p
		}
	}
	gateway.RegisterRoute(gateway.RouteDef{
		Path:    "{{ .path }}",
		Handler: handleOAuthCallback,
	})
}

// GenerateOAuthNonce creates a CSRF nonce for the given provider.
func GenerateOAuthNonce(provider string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return provider
	}
	state := hex.EncodeToString(b) + ":" + provider
	oauthNoncesMu.Lock()
	oauthNonces[state] = provider
	oauthNoncesMu.Unlock()
	return state
}

// OAuthCallbackURL returns the full callback URL.
func OAuthCallbackURL() string {
	return oauthCallbackPublicURL + "{{ .path }}"
}

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}
	if state == "" {
		http.Error(w, "missing state parameter", http.StatusBadRequest)
		return
	}
	// Validate CSRF nonce
	oauthNoncesMu.Lock()
	providerName, valid := oauthNonces[state]
	if valid {
		delete(oauthNonces, state)
	}
	oauthNoncesMu.Unlock()
	if !valid {
		http.Error(w, "invalid or expired state", http.StatusForbidden)
		return
	}
	provider, ok := oauthCallbackProviders[providerName]
	if !ok {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	if provider.TokenEndpoint == "" {
		http.Error(w, "provider not configured", http.StatusInternalServerError)
		return
	}
	redirectURI := OAuthCallbackURL()
	token, err := exchangeCodeForToken(provider, code, redirectURI)
	if err != nil {
		slog.Error("oauth-callback: token exchange failed", "provider", providerName, "error", err)
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}
	tokenFile := oauthCallbackTokenDir + "/" + providerName + ".json"
	if err := writeOAuthToken(tokenFile, token, provider); err != nil {
		slog.Error("oauth-callback: failed to save token", "provider", providerName, "error", err)
		http.Error(w, "failed to save token", http.StatusInternalServerError)
		return
	}
	gateway.RegisterSecret(token.AccessToken)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!DOCTYPE html><html><body>
<h1>Authorization successful</h1>
<p>Provider <strong>%s</strong> has been connected. You can close this tab.</p>
</body></html>`, html.EscapeString(providerName))
}

type oauthTokenExchangeResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func exchangeCodeForToken(provider oauthCallbackConfig, code, redirectURI string) (*oauthTokenExchangeResponse, error) {
	u, err := url.Parse(provider.TokenEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid token_endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("token_endpoint must use https, got %q", u.Scheme)
	}
	params := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"client_id":    {provider.ClientID},
		"redirect_uri": {redirectURI},
	}
	if provider.ClientSecret != "" {
		params.Set("client_secret", provider.ClientSecret)
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: callbackSSRFSafeTransport(),
	}
	resp, err := client.Post(provider.TokenEndpoint, "application/x-www-form-urlencoded", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	var tr oauthTokenExchangeResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &tr, nil
}

func writeOAuthToken(path string, token *oauthTokenExchangeResponse, provider oauthCallbackConfig) error {
	expiresIn := token.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 3600
	}
	stored := map[string]any{
		"access_token":   token.AccessToken,
		"expires_at":     time.Now().Unix() + expiresIn,
		"token_endpoint": provider.TokenEndpoint,
		"client_id":      provider.ClientID,
	}
	if token.RefreshToken != "" {
		stored["refresh_token"] = token.RefreshToken
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func callbackSSRFSafeTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q: %w", addr, err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
			}
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
					return nil, fmt.Errorf("refusing to connect to private IP %s", ip.IP)
				}
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}
