// Package mitm provides MITM rewriters for the gateway proxy.
// This file implements the OAuthRewriter which reads a stored OAuth token from
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
	"slices"
	"strings"
	"sync"
	"time"
)

// StoredToken represents a persisted OAuth token (written by setup, read/updated by this rewriter).
type StoredToken struct {
	AccessToken   string  `json:"access_token"`
	RefreshToken  *string `json:"refresh_token"`
	ExpiresAt     int64   `json:"expires_at"`
	TokenEndpoint string  `json:"token_endpoint"`
	ClientID      string  `json:"client_id"`
	ClientSecret  *string `json:"client_secret"`
}

// OAuthRewriter injects a Bearer token into requests destined for specific domains.
// It reads a token file from disk, refreshes the token when expired, and caches
// the current access token in memory.
type OAuthRewriter struct {
	domains   []string
	tokenFile string

	mu          sync.Mutex
	cachedToken *StoredToken
	cachedUntil time.Time
	httpClient  *http.Client
}

// NewOAuthRewriter creates a rewriter that reads an OAuth token file and injects
// Bearer tokens for the given domains. The token file must exist and contain valid
// JSON matching the StoredToken format.
func NewOAuthRewriter(domains []string, tokenFile string) (*OAuthRewriter, error) {
	if tokenFile == "" {
		return nil, fmt.Errorf("oauth: token_file is required")
	}

	r := &OAuthRewriter{
		domains:   domains,
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

	return r, nil
}

// RewriteRequest injects a Bearer Authorization header if the request host matches
// one of the configured domains. Returns true if the header was injected.
func (r *OAuthRewriter) RewriteRequest(req *http.Request) bool {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	matched := slices.Contains(r.domains, host)
	if !matched {
		return false
	}

	token, err := r.getValidToken()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("oauth: token file not found (not yet authorized)", "host", host, "file", r.tokenFile)
		} else {
			slog.Error("oauth: failed to get token", "error", err, "host", host)
		}
		return false
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return true
}

// getValidToken returns a valid access token, refreshing if necessary.
func (r *OAuthRewriter) getValidToken() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Use cached token if still valid (with 5-minute buffer).
	if r.cachedToken != nil && time.Now().Before(r.cachedUntil) {
		return r.cachedToken.AccessToken, nil
	}

	// Read token from file.
	stored, err := r.readTokenFile()
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()

	// Refresh if token expires within 5 minutes.
	if now+300 >= stored.ExpiresAt {
		refreshed, err := r.refreshToken(stored)
		if err != nil {
			return "", fmt.Errorf("token refresh failed: %w", err)
		}
		stored = refreshed

		// Save refreshed token back to file.
		if err := r.writeTokenFile(stored); err != nil {
			slog.Error("oauth: failed to write refreshed token", "error", err)
			// Non-fatal — token is still usable in memory.
		}
	}

	// Cache until 5 minutes before expiry (minimum 60 seconds).
	ttl := max(stored.ExpiresAt-now-300, 60)
	r.cachedToken = stored
	r.cachedUntil = time.Now().Add(time.Duration(ttl) * time.Second)

	return stored.AccessToken, nil
}

// readTokenFile reads and parses the stored token JSON file.
func (r *OAuthRewriter) readTokenFile() (*StoredToken, error) {
	data, err := os.ReadFile(r.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading token file %s: %w", r.tokenFile, err)
	}

	var token StoredToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing token file %s: %w", r.tokenFile, err)
	}

	return &token, nil
}

// writeTokenFile writes the refreshed token back to disk using write-rename
// for crash safety (avoids partial writes corrupting the JSON file).
func (r *OAuthRewriter) writeTokenFile(token *StoredToken) error {
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.tokenFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.tokenFile)
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

			// All IPs are public — connect to the first one.
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}

// refreshToken exchanges a refresh token for a new access token.
func (r *OAuthRewriter) refreshToken(stored *StoredToken) (*StoredToken, error) {
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

	resp, err := r.httpClient.Post(
		stored.TokenEndpoint,
		"application/x-www-form-urlencoded",
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("refresh request to %s: %w", stored.TokenEndpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB max
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

	// Preserve old refresh token if server didn't return a new one.
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
