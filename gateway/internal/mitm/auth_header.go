package mitm

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
)

// AuthHeaderRewriter injects a header into requests destined for specific domains.
// The header value is read from an environment variable and formatted using a template
// string where ${value} is replaced with the env var value (e.g., "token ${value}").
// Special placeholder ${base64_basic} is replaced with base64("x-access-token:<value>"),
// which is the format required by git HTTP authentication.
type AuthHeaderRewriter struct {
	domains     []string
	header      string
	headerValue string // pre-computed final header value
}

// NewAuthHeaderRewriter creates a rewriter that injects a header for the given domains.
// envVar is the environment variable holding the secret value.
// valueFormat is the header value template, e.g. "token ${value}" or "Basic ${base64_basic}".
// If valueFormat is empty, it defaults to "${value}".
func NewAuthHeaderRewriter(domains []string, header, valueFormat, envVar string) (*AuthHeaderRewriter, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("%s not set", envVar)
	}
	if valueFormat == "" {
		valueFormat = "${value}"
	}

	// Compute the final header value with all substitutions
	headerValue := valueFormat
	headerValue = strings.ReplaceAll(headerValue, "${base64_basic}", base64.StdEncoding.EncodeToString([]byte("x-access-token:"+value)))
	headerValue = strings.ReplaceAll(headerValue, "${value}", value)

	return &AuthHeaderRewriter{
		domains:     domains,
		header:      header,
		headerValue: headerValue,
	}, nil
}

// RewriteRequest injects the configured header if the request host matches one of the
// configured domains. Returns true if the header was injected.
func (r *AuthHeaderRewriter) RewriteRequest(req *http.Request) bool {
	host := req.Host
	// Strip port if present (e.g., "api.github.com:443" → "api.github.com")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	matched := slices.Contains(r.domains, host)
	if !matched {
		return false
	}

	req.Header.Set(r.header, r.headerValue)
	return true
}
