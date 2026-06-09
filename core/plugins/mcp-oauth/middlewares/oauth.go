package custom

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

type storedToken struct {
	AccessToken   string  `json:"access_token"`
	RefreshToken  *string `json:"refresh_token"`
	ExpiresAt     int64   `json:"expires_at"`
	TokenEndpoint string  `json:"token_endpoint"`
	ClientID      string  `json:"client_id"`
}

type oauthProviderInfo struct {
	AuthorizeEndpoint string
	TokenEndpoint     string
	ClientID          string
	ClientSecret      string
	Scopes            string
	MCPURL            string
	Dynamic           bool
}

type oauthState struct {
	tokenDir    string
	providers   map[string]oauthProviderInfo
	mu          sync.Mutex
	cachedToken *storedToken
	cachedUntil time.Time
	httpClient  *http.Client
}

var oauthHMACKey []byte

func init() {
	providersJSON := `{{ toJSON .options.providers }}`
	h := sha256.Sum256([]byte(providersJSON))
	oauthHMACKey = h[:]

	tokenDir := "{{ .options.token_dir }}"
	callbackURL := "{{ .options.callback_url }}"

	providers := make(map[string]oauthProviderInfo)
	var rawProviders map[string]map[string]any
	if err := json.Unmarshal([]byte(providersJSON), &rawProviders); err != nil {
		slog.Error("oauth: failed to parse providers config", "error", err)
	} else {
		for name, cfg := range rawProviders {
			p := oauthProviderInfo{}
			if v, ok := cfg["mcp_url"].(string); ok {
				p.MCPURL = v
			}
			if v, ok := cfg["authorize_endpoint"].(string); ok {
				p.AuthorizeEndpoint = v
			}
			if v, ok := cfg["token_endpoint"].(string); ok {
				p.TokenEndpoint = v
			}
			if v, ok := cfg["client_id"].(string); ok {
				p.ClientID = v
			}
			if v, ok := cfg["client_secret"].(string); ok {
				p.ClientSecret = v
			}
			if v, ok := cfg["scopes"].(string); ok {
				p.Scopes = v
			}
			// Dynamic mode: no client_id means we need discovery+registration
			if p.ClientID == "" {
				p.Dynamic = true
				mcpURL, _ := cfg["mcp_url"].(string)
				resolved := resolveProvider(mcpURL, callbackURL, tokenDir, name)
				if resolved != nil {
					p.AuthorizeEndpoint = resolved.AuthorizeEndpoint
					p.TokenEndpoint = resolved.TokenEndpoint
					p.ClientID = resolved.ClientID
					p.ClientSecret = resolved.ClientSecret
				}
			}
			providers[name] = p
		}
	}

	// Register one middleware per provider, scoped to that provider's domain
	state := &oauthState{
		tokenDir:  tokenDir,
		providers: providers,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: oauthSSRFSafeTransport(),
		},
	}

	for name, p := range providers {
		providerName := name
		provider := p
		providerTokenFile := tokenDir + "/" + providerName + ".json"

		// Extract domain from mcp_url for this provider
		var providerDomain string
		if raw, ok := rawProviders[providerName]; ok {
			if mcpURL, ok := raw["mcp_url"].(string); ok {
				if u, err := url.Parse(mcpURL); err == nil {
					providerDomain = u.Hostname()
				}
			}
		}
		if providerDomain == "" {
			continue
		}

		// Register secrets for this provider's token
		for _, s := range oauthSecrets(providerTokenFile) {
			gateway.RegisterSecret(s)
		}

		gateway.RegisterMiddleware(gateway.MiddlewareDef{
			Name:    "oauth:" + providerName,
			Domains: []string{providerDomain},
			Func: func(ctx *gateway.MiddlewareContext) error {
				token, err := state.getValidToken(providerTokenFile)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						if provider.AuthorizeEndpoint != "" {
							authorizeURL := mwBuildAuthorizeURL(provider, providerName, callbackURL)
							ctx.SetAbortHeader("X-OAuth-Authorize-URL", authorizeURL)
							ctx.SetAbortHeader("Content-Type", "application/json")
							ctx.Abort(http.StatusUnauthorized, fmt.Sprintf(
								`{"error":"oauth_required","provider":%q,"authorize_url":%q,"hint":"For PKCE login, use: curl http://<gateway>/plugins/mcp-oauth/login/%s"}`,
								providerName, authorizeURL, providerName))
							return nil
						}
						slog.Debug("oauth: token file not found", "file", providerTokenFile)
					} else {
						slog.Error("oauth: failed to get token", "provider", providerName, "error", err)
					}
					return nil
				}
				ctx.Request.Header.Set("Authorization", "Bearer "+token)
				return nil
			},
		})
	}
}

// resolveProvider does OAuth metadata discovery + dynamic client registration.
// Returns nil if discovery fails (provider will be skipped).
func resolveProvider(mcpURL, callbackURL, tokenDir, name string) *oauthProviderInfo {
	if mcpURL == "" {
		slog.Error("oauth: dynamic provider has no mcp_url", "provider", name)
		return nil
	}

	// Check for cached registration
	regFile := tokenDir + "/" + name + ".reg.json"
	if data, err := os.ReadFile(regFile); err == nil {
		var cached struct {
			AuthorizeEndpoint string `json:"authorize_endpoint"`
			TokenEndpoint     string `json:"token_endpoint"`
			ClientID          string `json:"client_id"`
			ClientSecret      string `json:"client_secret"`
		}
		if json.Unmarshal(data, &cached) == nil && cached.ClientID != "" {
			slog.Info("oauth: using cached dynamic registration", "provider", name)
			return &oauthProviderInfo{
				AuthorizeEndpoint: cached.AuthorizeEndpoint,
				TokenEndpoint:     cached.TokenEndpoint,
				ClientID:          cached.ClientID,
				ClientSecret:      cached.ClientSecret,
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	meta, err := gateway.DiscoverOAuthMetadata(ctx, mcpURL)
	if err != nil {
		slog.Error("oauth: metadata discovery failed", "provider", name, "error", err)
		return nil
	}

	reg, err := gateway.RegisterOAuthClient(ctx, meta.RegistrationEndpoint, []string{callbackURL}, "agent-sandbox:"+name)
	if err != nil {
		slog.Error("oauth: dynamic registration failed", "provider", name, "error", err)
		return nil
	}

	// Persist registration for reuse across restarts
	regData, _ := json.MarshalIndent(map[string]string{
		"authorize_endpoint": meta.AuthorizationEndpoint,
		"token_endpoint":     meta.TokenEndpoint,
		"client_id":          reg.ClientID,
		"client_secret":      reg.ClientSecret,
	}, "", "  ")
	if err := os.WriteFile(regFile, regData, 0600); err != nil {
		slog.Warn("oauth: failed to cache registration", "provider", name, "error", err)
	}

	slog.Info("oauth: dynamic registration complete", "provider", name, "client_id", reg.ClientID)
	return &oauthProviderInfo{
		AuthorizeEndpoint: meta.AuthorizationEndpoint,
		TokenEndpoint:     meta.TokenEndpoint,
		ClientID:          reg.ClientID,
		ClientSecret:      reg.ClientSecret,
	}
}

func mwBuildAuthorizeURL(provider oauthProviderInfo, providerName, callbackURL string) string {
	mac := hmac.New(sha256.New, oauthHMACKey)
	mac.Write([]byte(providerName))
	sig := hex.EncodeToString(mac.Sum(nil))[:16]
	state := sig + ":" + providerName

	params := url.Values{
		"client_id":     {provider.ClientID},
		"response_type": {"code"},
		"state":         {state},
		"redirect_uri":  {callbackURL},
	}
	if provider.Scopes != "" {
		params.Set("scope", provider.Scopes)
	}
	if provider.MCPURL != "" {
		params.Set("resource", provider.MCPURL)
	}
	return provider.AuthorizeEndpoint + "?" + params.Encode()
}

func oauthSecrets(tokenFile string) []string {
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil
	}
	var token storedToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil
	}
	if token.AccessToken != "" {
		return []string{token.AccessToken}
	}
	return nil
}

func (s *oauthState) getValidToken(tokenFile string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedToken != nil && time.Now().Before(s.cachedUntil) {
		return s.cachedToken.AccessToken, nil
	}
	stored, err := s.readTokenFile(tokenFile)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if now+300 >= stored.ExpiresAt {
		refreshed, err := s.refreshToken(stored)
		if err != nil {
			return "", fmt.Errorf("token refresh failed: %w", err)
		}
		stored = refreshed
		if err := s.writeTokenFile(tokenFile, stored); err != nil {
			slog.Error("oauth: failed to write refreshed token", "error", err)
		}
	}
	ttl := stored.ExpiresAt - now - 300
	if ttl < 60 {
		ttl = 60
	}
	s.cachedToken = stored
	s.cachedUntil = time.Now().Add(time.Duration(ttl) * time.Second)
	gateway.RegisterSecret(stored.AccessToken)
	return stored.AccessToken, nil
}

func (s *oauthState) readTokenFile(tokenFile string) (*storedToken, error) {
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading token file %s: %w", tokenFile, err)
	}
	var token storedToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing token file %s: %w", tokenFile, err)
	}
	return &token, nil
}

func (s *oauthState) writeTokenFile(tokenFile string, token *storedToken) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	tmp := tokenFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, tokenFile)
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (s *oauthState) refreshToken(stored *storedToken) (*storedToken, error) {
	if stored.RefreshToken == nil || *stored.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh_token available — re-run oauth setup")
	}
	u, err := url.Parse(stored.TokenEndpoint)
	if err != nil {
		return nil, fmt.Errorf("oauth: invalid token_endpoint URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("oauth: token_endpoint must use https, got %q", u.Scheme)
	}
	params := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {*stored.RefreshToken},
		"client_id":     {stored.ClientID},
	}
	resp, err := s.httpClient.Post(
		stored.TokenEndpoint,
		"application/x-www-form-urlencoded",
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh returned %d", resp.StatusCode)
	}
	var tr oauthTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parsing refresh response: %w", err)
	}
	expiresIn := tr.ExpiresIn
	if expiresIn == 0 {
		expiresIn = 3600
	}
	refreshToken := stored.RefreshToken
	if tr.RefreshToken != "" {
		refreshToken = &tr.RefreshToken
	}
	return &storedToken{
		AccessToken:   tr.AccessToken,
		RefreshToken:  refreshToken,
		ExpiresAt:     time.Now().Unix() + expiresIn,
		TokenEndpoint: stored.TokenEndpoint,
		ClientID:      stored.ClientID,
	}, nil
}

func oauthSSRFSafeTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("oauth: invalid address %q: %w", addr, err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("oauth: DNS lookup failed for %q: %w", host, err)
			}
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
					return nil, fmt.Errorf("oauth: refusing to connect to private IP %s", ip.IP)
				}
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}
