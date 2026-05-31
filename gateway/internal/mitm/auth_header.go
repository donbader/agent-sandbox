package mitm

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

// AuthHeaderRewriter injects a header into requests destined for specific domains.
// The header value is read from an environment variable and formatted using a template
// string where ${value} is replaced with the env var value (e.g., "token ${value}").
type AuthHeaderRewriter struct {
	domains     []string
	header      string
	valueFormat string
	value       string
}

// NewAuthHeaderRewriter creates a rewriter that injects a header for the given domains.
// envVar is the environment variable holding the secret value.
// valueFormat is the header value template, e.g. "token ${value}" or "Bearer ${value}".
// If valueFormat is empty, it defaults to "${value}".
func NewAuthHeaderRewriter(domains []string, header, valueFormat, envVar string) (*AuthHeaderRewriter, error) {
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("%s not set", envVar)
	}
	if valueFormat == "" {
		valueFormat = "${value}"
	}
	return &AuthHeaderRewriter{
		domains:     domains,
		header:      header,
		valueFormat: valueFormat,
		value:       value,
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

	matched := false
	for _, d := range r.domains {
		if host == d {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}

	headerValue := strings.ReplaceAll(r.valueFormat, "${value}", r.value)
	req.Header.Set(r.header, headerValue)
	return true
}
