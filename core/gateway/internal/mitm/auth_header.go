package mitm

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

// RegisterAuthHeaderMiddleware creates and registers an auth-header middleware.
// It reads the secret from an environment variable at startup and injects it as
// an HTTP header for matching domains.
//
// valueFormat supports placeholders:
//   - ${value}: replaced with the raw env var value
//   - ${base64_basic}: replaced with base64("x-access-token:<value>") for git HTTP auth
func RegisterAuthHeaderMiddleware(name string, domains []string, header, valueFormat, envVar string) error {
	value := os.Getenv(envVar)
	if value == "" {
		return fmt.Errorf("%s not set", envVar)
	}
	if valueFormat == "" {
		valueFormat = "${value}"
	}

	// Compute the final header value with all substitutions
	headerValue := valueFormat
	headerValue = strings.ReplaceAll(headerValue, "${base64_basic}", base64.StdEncoding.EncodeToString([]byte("x-access-token:"+value)))
	headerValue = strings.ReplaceAll(headerValue, "${value}", value)

	// Register the secret for log redaction
	gateway.RegisterSecret(value)

	gateway.RegisterMiddleware(gateway.MiddlewareDef{
		Name:    name,
		Domains: domains,
		Func: func(ctx *gateway.MiddlewareContext) error {
			ctx.Request.Header.Set(header, headerValue)
			return nil
		},
	})

	return nil
}
