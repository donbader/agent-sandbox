// Package mitm provides built-in middleware for the gateway proxy.
// This file implements the OAuth middleware which reads a stored OAuth token from
// a JSON file, refreshes it when expired, and injects a Bearer Authorization header.
package mitm

import (
	"context"
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

// StoredToken represents a persisted OAuth token (written by setup, read/updated by this middleware).
type StoredToken struct {
	AccessToken   string  `json:"access_token"`
	RefreshToken  *string `json:"refresh_token"`
	ExpiresAt     int64   `json:"expires_at"`
	TokenEndpoint string  `json:"token_endpoint"`
	ClientID      string  `json:"client_id"`
	ClientSecret  *string `json:"client_secret"`
}

// oauthState holds the runtime state for the OAuth middleware.
type oauthState struct {
	tokenFile   string
	mu          sync.Mutex
	cachedToken *StoredToken
	cachedUntil time.Time
	httpClient  *http.Client
}

// RegisterOAuthMiddleware creates and registers an OAuth middleware.
// It reads a token file from disk, refreshes the token when expired, and injects
// a Bearer Authorization header for matching domains.
func RegisterOAuthMiddleware(name string, domains []string, tokenFile string) error {
	if tokenFile == "" {
		return fmt.Errorf("oauth: token_file is required")
	}

	state := &oauthState{
		tokenFile: tokenFile,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: ssrfSafeTransport(),
		},
	}

	// Verify file is readable at startup (non-fatal — file might appear later via setup).
	if _, err := os.Stat(tokenFile); err != nil {
		slog.Warn("oauth token file not found at startup", "path", tokenFile, "error", err)
	}

	gateway.RegisterMiddleware(gateway.MiddlewareDef{
		Name:    name,
		Domains: domains,
		Func: func(ctx *gateway.MiddlewareContext) error {
			token, err := state.getValidToken()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					slog.Debug("oauth: token file not found (not yet authorized)", "file", state.tokenFile)
				} else {
					slog.Error("oauth: failed to get token", "error", err)
				}
				return nil // non-fatal: request continues without auth
			}

			ctx.Request.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
	})

	return nil
}

// OAuthSecrets returns current OAuth token secrets for log redaction.
func OAuthSecrets(tokenFile string) []string {
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil
	}
	var token StoredToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil
	}
	if token.AccessToken != "" {
		return []string{token.AccessToken}
	}
	return nil
}

// getValidToken returns a valid access token, refreshing if necessary.
func (s *oauthState) getValidToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cachedToken != nil && time.Now().Before(s.cachedUntil) {
		return s.cachedToken.AccessToken, nil
	}

	stored, err := s.readTokenFile()
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
		if err := s.writeTokenFile(stored); err != nil {
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

// readTokenFile reads and parses the stored token JSON file.
func (s *oauthState) readTokenFile() (*StoredToken, error) {
	data, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading token file %s: %w", s.tokenFile, err)
	}
	var token StoredToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing token file %s: %w", s.tokenFile, err)
	}
	return &token, nil
}

// writeTokenFile writes the refreshed token back to disk using write-rename.
func (s *oauthState) writeTokenFile(token *StoredToken) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.tokenFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.tokenFile)
}

// tokenResponse is the OAuth token endpoint response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// validateTokenEndpoint checks that the token endpoint URL is safe to call.
func validateTokenEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("oauth: invalid token_endpoint URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("oauth: token_endpoint must use https, got %q", u.Scheme)
	}
	return nil
}

// isPrivateIP returns true if the IP is in a private, loopback, or link-local range.
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// ssrfSafeTransport returns an http.Transport with a custom DialContext that blocks
// connections to private/internal IP addresses (SSRF protection).
func ssrfSafeTransport() *http.Transport {
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
				if isPrivateIP(ip.IP) {
					return nil, fmt.Errorf("oauth: refusing to connect to private IP %s (resolved from %s)", ip.IP, host)
				}
			}

			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}

// refreshToken exchanges a refresh token for a new access token.
func (s *oauthState) refreshToken(stored *StoredToken) (*StoredToken, error) {
	if stored.RefreshToken == nil || *stored.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh_token available — re-run oauth setup")
	}

	if err := validateTokenEndpoint(stored.TokenEndpoint); err != nil {
		return nil, err
	}

	params := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {*stored.RefreshToken},
		"client_id":     {stored.ClientID},
	}
	if stored.ClientSecret != nil && *stored.ClientSecret != "" {
		params.Set("client_secret", *stored.ClientSecret)
	}

	resp, err := s.httpClient.Post(
		stored.TokenEndpoint,
		"application/x-www-form-urlencoded",
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("refresh request to %s: %w", stored.TokenEndpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh returned %d: %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
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

	return &StoredToken{
		AccessToken:   tr.AccessToken,
		RefreshToken:  refreshToken,
		ExpiresAt:     time.Now().Unix() + expiresIn,
		TokenEndpoint: stored.TokenEndpoint,
		ClientID:      stored.ClientID,
		ClientSecret:  stored.ClientSecret,
	}, nil
}
