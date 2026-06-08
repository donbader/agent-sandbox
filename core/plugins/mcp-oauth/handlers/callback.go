package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

// oauthProviderConfig holds OAuth provider settings baked in at generate time.
type oauthProviderConfig struct {
	TokenEndpoint string
	ClientID      string
	ClientSecret  string
	MCP_URL       string
}

var oauthCallbackProviders = map[string]oauthProviderConfig{}
var oauthCallbackTokenDir string

func init() {
	oauthCallbackTokenDir = "{{ .options.token_dir }}"
	providersJSON := `{{ toJSON .options.providers }}`
	var providers map[string]map[string]any
	if err := json.Unmarshal([]byte(providersJSON), &providers); err == nil {
		for name, cfg := range providers {
			p := oauthProviderConfig{}
			if v, ok := cfg["token_endpoint"].(string); ok {
				p.TokenEndpoint = v
			}
			if v, ok := cfg["client_id"].(string); ok {
				p.ClientID = v
			}
			if v, ok := cfg["client_secret"].(string); ok {
				p.ClientSecret = v
			}
			if v, ok := cfg["mcp_url"].(string); ok {
				p.MCP_URL = v
			}
			oauthCallbackProviders[name] = p
		}
	}

	gateway.RegisterRoute(gateway.RouteDef{
		Path:    "{{ .path }}",
		Handler: handleOAuthCallback,
	})
}

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state") // state = provider name

	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}
	if state == "" {
		http.Error(w, "missing state parameter (provider name)", http.StatusBadRequest)
		return
	}

	provider, ok := oauthCallbackProviders[state]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown provider: %s", state), http.StatusBadRequest)
		return
	}

	if provider.TokenEndpoint == "" {
		http.Error(w, fmt.Sprintf("provider %s has no token_endpoint configured", state), http.StatusInternalServerError)
		return
	}

	// Exchange authorization code for token
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		// Reconstruct from request
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		redirectURI = fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.Path)
	}

	token, err := exchangeCode(provider, code, redirectURI)
	if err != nil {
		http.Error(w, fmt.Sprintf("token exchange failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Write token file
	tokenFile := oauthCallbackTokenDir + "/" + state + ".json"
	if err := writeCallbackToken(tokenFile, token, provider); err != nil {
		http.Error(w, fmt.Sprintf("failed to save token: %v", err), http.StatusInternalServerError)
		return
	}

	// Register the new access token as a secret for log redaction
	gateway.RegisterSecret(token.AccessToken)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!DOCTYPE html><html><body>
<h1>Authorization successful</h1>
<p>Provider <strong>%s</strong> has been connected. You can close this tab.</p>
</body></html>`, state)
}

type callbackTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func exchangeCode(provider oauthProviderConfig, code, redirectURI string) (*callbackTokenResponse, error) {
	u, err := url.Parse(provider.TokenEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid token_endpoint URL: %w", err)
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

	resp, err := client.Post(
		provider.TokenEndpoint,
		"application/x-www-form-urlencoded",
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("token request to %s: %w", provider.TokenEndpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tr callbackTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	return &tr, nil
}

func writeCallbackToken(path string, token *callbackTokenResponse, provider oauthProviderConfig) error {
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
	if provider.ClientSecret != "" {
		stored["client_secret"] = provider.ClientSecret
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
					return nil, fmt.Errorf("refusing to connect to private IP %s (resolved from %s)", ip.IP, host)
				}
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}
