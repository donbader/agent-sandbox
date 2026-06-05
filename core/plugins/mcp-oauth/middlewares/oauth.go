package custom

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

type storedToken struct {
	AccessToken   string  `json:"access_token"`
	RefreshToken  *string `json:"refresh_token"`
	ExpiresAt     int64   `json:"expires_at"`
	TokenEndpoint string  `json:"token_endpoint"`
	ClientID      string  `json:"client_id"`
	ClientSecret  *string `json:"client_secret"`
}

type oauthState struct {
	tokenFile   string
	mu          sync.Mutex
	cachedToken *storedToken
	cachedUntil time.Time
	httpClient  *http.Client
}

func init() {
	tokenFile := "{{ .options.token_file }}"
	domains := strings.Split("{{ .domainsList }}", ",")

	state := &oauthState{
		tokenFile: tokenFile,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: oauthSSRFSafeTransport(),
		},
	}

	if _, err := os.Stat(tokenFile); err != nil {
		slog.Warn("oauth token file not found at startup", "path", tokenFile, "error", err)
	}
	for _, s := range oauthSecrets(tokenFile) {
		gateway.RegisterSecret(s)
	}

	gateway.RegisterMiddleware(gateway.MiddlewareDef{
		Name:    "oauth:" + domains[0],
		Domains: domains,
		Func: func(ctx *gateway.MiddlewareContext) error {
			token, err := state.getValidToken()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					slog.Debug("oauth: token file not found", "file", state.tokenFile)
				} else {
					slog.Error("oauth: failed to get token", "error", err)
				}
				return nil
			}
			ctx.Request.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
	})
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

func (s *oauthState) readTokenFile() (*storedToken, error) {
	data, err := os.ReadFile(s.tokenFile)
	if err != nil {
		return nil, fmt.Errorf("reading token file %s: %w", s.tokenFile, err)
	}
	var token storedToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parsing token file %s: %w", s.tokenFile, err)
	}
	return &token, nil
}

func (s *oauthState) writeTokenFile(token *storedToken) error {
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
		ClientSecret:  stored.ClientSecret,
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
					return nil, fmt.Errorf("oauth: refusing to connect to private IP %s (resolved from %s)", ip.IP, host)
				}
			}
			dialer := &net.Dialer{Timeout: 10 * time.Second}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}
