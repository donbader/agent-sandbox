package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthMetadata holds discovered OAuth server metadata (RFC 8414).
type OAuthMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// OAuthRegistration holds dynamic client registration response (RFC 7591).
type OAuthRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
}

// DiscoverOAuthMetadata fetches OAuth server metadata from the MCP server.
// Tries .well-known/oauth-authorization-server first, falls back to .well-known/openid-configuration.
func DiscoverOAuthMetadata(ctx context.Context, mcpURL string) (*OAuthMetadata, error) {
	base, err := url.Parse(mcpURL)
	if err != nil {
		return nil, fmt.Errorf("parse mcp_url: %w", err)
	}

	// Try RFC 8414 path first
	wellKnownPaths := []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/openid-configuration",
	}

	client := &http.Client{Timeout: 15 * time.Second}

	for _, path := range wellKnownPaths {
		metaURL := base.Scheme + "://" + base.Host + path
		req, err := http.NewRequestWithContext(ctx, "GET", metaURL, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue
		}

		var meta OAuthMetadata
		if err := json.Unmarshal(body, &meta); err != nil {
			continue
		}
		if meta.AuthorizationEndpoint != "" && meta.TokenEndpoint != "" {
			return &meta, nil
		}
	}

	return nil, fmt.Errorf("oauth metadata not found at %s", base.Host)
}

// RegisterOAuthClient performs Dynamic Client Registration (RFC 7591).
func RegisterOAuthClient(ctx context.Context, registrationEndpoint string, redirectURIs []string, clientName string) (*OAuthRegistration, error) {
	if registrationEndpoint == "" {
		return nil, fmt.Errorf("no registration_endpoint available")
	}

	u, err := url.Parse(registrationEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid registration_endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("registration_endpoint must use https, got %q", u.Scheme)
	}

	reqBody := map[string]any{
		"client_name":   clientName,
		"redirect_uris": redirectURIs,
		"grant_types":   []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"},
		"token_endpoint_auth_method": "client_secret_post",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal registration request: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", registrationEndpoint, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registration request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading registration response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("registration returned %d: %s", resp.StatusCode, string(body))
	}

	var reg OAuthRegistration
	if err := json.Unmarshal(body, &reg); err != nil {
		return nil, fmt.Errorf("parse registration response: %w", err)
	}
	if reg.ClientID == "" {
		return nil, fmt.Errorf("registration response missing client_id")
	}

	return &reg, nil
}
