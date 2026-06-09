package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

type loginProviderConfig struct {
	MCPURL            string
	AuthorizeEndpoint string
	TokenEndpoint     string
	ClientID          string
	ClientSecret      string
	Scopes            string
}

var (
	loginProviders   map[string]*loginProviderConfig
	loginTokenDir    string
	loginCallbackURL string
	loginRegMu       sync.Mutex
)

func init() {
	loginTokenDir = "{{ .options.token_dir }}"
	loginCallbackURL = "{{ .options.callback_url }}"
	providersJSON := `{{ toJSON .options.providers }}`

	loginProviders = make(map[string]*loginProviderConfig)
	var rawProviders map[string]map[string]any
	if err := json.Unmarshal([]byte(providersJSON), &rawProviders); err != nil {
		slog.Error("oauth-login: failed to parse providers", "error", err)
		return
	}

	for name, cfg := range rawProviders {
		p := &loginProviderConfig{}
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
		loginProviders[name] = p
	}

	gateway.RegisterRoute(gateway.RouteDef{
		Path:    "{{ .path }}",
		Handler: handleOAuthLogin,
	})
}

func handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	// Extract provider name from path: /plugins/mcp-oauth/login/{provider}
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "{{ .path }}"), "/")
	var providerName string
	for _, part := range pathParts {
		if part != "" {
			providerName = part
			break
		}
	}

	if providerName == "" {
		names := make([]string, 0, len(loginProviders))
		for name := range loginProviders {
			names = append(names, name)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     "provider name required",
			"available": names,
			"usage":     "GET {{ .path }}/<provider_name>",
		})
		return
	}

	provider, ok := loginProviders[providerName]
	if !ok {
		names := make([]string, 0, len(loginProviders))
		for name := range loginProviders {
			names = append(names, name)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     fmt.Sprintf("unknown provider: %s", providerName),
			"available": names,
		})
		return
	}

	// Determine callback URL: use configured value if set, otherwise derive from request
	callbackURL := loginCallbackURL
	if callbackURL == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		host := r.Host
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = fwd
		}
		callbackURL = scheme + "://" + host + "/plugins/mcp-oauth/callback"
	}

	// Ensure we have client credentials (DCR if needed)
	if err := ensureClientRegistration(providerName, provider, callbackURL); err != nil {
		slog.Error("oauth-login: registration failed", "provider", providerName, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("client registration failed: %v", err),
		})
		return
	}

	// Validate authorize endpoint uses HTTPS
	parsedAuth, err := url.Parse(provider.AuthorizeEndpoint)
	if err != nil || parsedAuth.Scheme != "https" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("authorize endpoint must use https, got %q", provider.AuthorizeEndpoint),
		})
		return
	}

	// Generate PKCE
	codeVerifier, err := GenerateCodeVerifier()
	if err != nil {
		http.Error(w, "failed to generate code verifier", http.StatusInternalServerError)
		return
	}
	codeChallenge := CodeChallengeS256(codeVerifier)

	// Generate state
	state, err := GenerateState()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}

	// Store pending flow
	StorePendingFlow(state, &PendingOAuthFlow{
		Provider:     providerName,
		CodeVerifier: codeVerifier,
		RedirectURI:  callbackURL,
		CreatedAt:    time.Now(),
	})

	// Build authorize URL
	params := url.Values{
		"client_id":             {provider.ClientID},
		"response_type":         {"code"},
		"state":                 {state},
		"redirect_uri":          {callbackURL},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	if provider.MCPURL != "" {
		params.Set("resource", provider.MCPURL)
	}
	if provider.Scopes != "" {
		params.Set("scope", provider.Scopes)
	}

	authorizeURL := provider.AuthorizeEndpoint + "?" + params.Encode()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authorize_url": authorizeURL,
		"provider":      providerName,
		"instructions":  "Open the authorize_url in your browser to complete login.",
	})
}

// ensureClientRegistration checks for cached credentials or performs DCR.
func ensureClientRegistration(name string, provider *loginProviderConfig, callbackURL string) error {
	if provider.ClientID != "" {
		return nil
	}

	loginRegMu.Lock()
	defer loginRegMu.Unlock()

	// Re-check after acquiring lock
	if provider.ClientID != "" {
		return nil
	}

	// Check cached registration file
	regFile := loginTokenDir + "/" + name + ".reg.json"
	if data, err := os.ReadFile(regFile); err == nil {
		var cached struct {
			AuthorizeEndpoint string `json:"authorize_endpoint"`
			TokenEndpoint     string `json:"token_endpoint"`
			ClientID          string `json:"client_id"`
			ClientSecret      string `json:"client_secret"`
		}
		if json.Unmarshal(data, &cached) == nil && cached.ClientID != "" {
			provider.AuthorizeEndpoint = cached.AuthorizeEndpoint
			provider.TokenEndpoint = cached.TokenEndpoint
			provider.ClientID = cached.ClientID
			provider.ClientSecret = cached.ClientSecret
			return nil
		}
	}

	// Perform discovery + DCR
	if provider.MCPURL == "" {
		return fmt.Errorf("no mcp_url configured for provider %s", name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	meta, err := gateway.DiscoverOAuthMetadata(ctx, provider.MCPURL)
	if err != nil {
		return fmt.Errorf("metadata discovery: %w", err)
	}

	reg, err := gateway.RegisterOAuthClient(ctx, meta.RegistrationEndpoint, []string{callbackURL}, "agent-sandbox:"+name)
	if err != nil {
		return fmt.Errorf("dynamic registration: %w", err)
	}

	// Update in-memory config
	provider.AuthorizeEndpoint = meta.AuthorizationEndpoint
	provider.TokenEndpoint = meta.TokenEndpoint
	provider.ClientID = reg.ClientID
	provider.ClientSecret = reg.ClientSecret

	// Persist for reuse across restarts
	regData, _ := json.MarshalIndent(map[string]string{
		"authorize_endpoint": meta.AuthorizationEndpoint,
		"token_endpoint":     meta.TokenEndpoint,
		"client_id":          reg.ClientID,
		"client_secret":      reg.ClientSecret,
	}, "", "  ")
	if err := os.MkdirAll(loginTokenDir, 0700); err != nil {
		slog.Warn("oauth-login: failed to create token dir", "error", err)
	}
	if err := os.WriteFile(regFile, regData, 0600); err != nil {
		slog.Warn("oauth-login: failed to cache registration", "error", err)
	}

	return nil
}
