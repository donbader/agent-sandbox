package redact

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_RedactsSecretInMessage(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"super-secret-token"})
	logger := slog.New(h)

	logger.Info("connecting to /bot" + "super-secret-token" + "/getUpdates")

	assert.Contains(t, buf.String(), "[REDACTED]")
	assert.NotContains(t, buf.String(), "super-secret-token")
}

func TestHandler_RedactsSecretInAttrValue(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"my-api-key-123"})
	logger := slog.New(h)

	logger.Info("request", "path", "/api/my-api-key-123/resource", "host", "example.com")

	assert.Contains(t, buf.String(), "[REDACTED]")
	assert.NotContains(t, buf.String(), "my-api-key-123")
	// Non-secret attrs pass through.
	assert.Contains(t, buf.String(), "example.com")
}

func TestHandler_RedactsMultipleSecrets(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"secret-one", "secret-two"})
	logger := slog.New(h)

	logger.Info("both secrets", "a", "has secret-one here", "b", "has secret-two here")

	assert.NotContains(t, buf.String(), "secret-one")
	assert.NotContains(t, buf.String(), "secret-two")
}

func TestHandler_IgnoresEmptySecrets(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"", "", "real-secret"})

	// Empty secrets should not cause everything to be redacted.
	assert.Len(t, h.baseSecret, 1)
	assert.Equal(t, "real-secret", h.baseSecret[0])
}

func TestHandler_PassesThroughWhenNoSecrets(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, nil)
	logger := slog.New(h)

	logger.Info("normal message", "key", "value")

	assert.Contains(t, buf.String(), "normal message")
	assert.Contains(t, buf.String(), "value")
}

func TestHandler_RedactsGroupAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"nested-secret"})
	logger := slog.New(h)

	logger.Info("grouped",
		slog.Group("req",
			slog.String("path", "/bot"+"nested-secret"+"/send"),
			slog.String("host", "api.telegram.org"),
		),
	)

	assert.NotContains(t, buf.String(), "nested-secret")
	assert.Contains(t, buf.String(), "api.telegram.org")
}

func TestHandler_WithAttrsRedacts(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"persistent-secret"})
	logger := slog.New(h).With("ctx", "has persistent-secret value")

	logger.Info("test")

	assert.NotContains(t, buf.String(), "persistent-secret")
	assert.Contains(t, buf.String(), "[REDACTED]")
}

func TestHandler_Enabled(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := NewHandler(inner, []string{"secret"})

	require.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	require.True(t, h.Enabled(context.Background(), slog.LevelWarn))
}

func TestHandler_ErrorValueRedacted(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})
	h := NewHandler(inner, []string{"leaked-token"})
	logger := slog.New(h)

	logger.Error("upstream failed", "error", "dial tcp: connection to https://api.example.com/leaked-token refused")

	assert.NotContains(t, buf.String(), "leaked-token")
	assert.Contains(t, buf.String(), "[REDACTED]")
}

func TestHandler_WithSecretsFunc_RedactsDynamicSecrets(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})

	// Start with no static secrets.
	h := NewHandler(inner, nil)

	// Simulate a dynamic secret registered after handler creation (e.g. by a TS plugin).
	dynamicSecrets := []string{"dynamic-bot-token-xyz"}
	h = h.WithSecretsFunc(func() []string { return dynamicSecrets })

	logger := slog.New(h)
	logger.Info("request", "path", "/bot"+"dynamic-bot-token-xyz"+"/getUpdates")

	assert.NotContains(t, buf.String(), "dynamic-bot-token-xyz")
	assert.Contains(t, buf.String(), "[REDACTED]")
}

func TestHandler_WithSecretsFunc_CombinesStaticAndDynamic(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})

	h := NewHandler(inner, []string{"static-secret"})
	h = h.WithSecretsFunc(func() []string { return []string{"dynamic-secret"} })

	logger := slog.New(h)
	logger.Info("both", "a", "has static-secret", "b", "has dynamic-secret")

	assert.NotContains(t, buf.String(), "static-secret")
	assert.NotContains(t, buf.String(), "dynamic-secret")
}

func TestHandler_WithSecretsFunc_PicksUpNewSecrets(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{})

	// Simulate a registry that grows over time.
	var registry []string
	h := NewHandler(inner, nil).WithSecretsFunc(func() []string { return registry })

	logger := slog.New(h)

	// First log: secret not yet registered — it will leak.
	logger.Info("before", "path", "/bot"+"late-registered-token"+"/send")
	assert.Contains(t, buf.String(), "late-registered-token")

	// Register the secret (as a plugin would on first request).
	registry = append(registry, "late-registered-token")
	buf.Reset()

	// Second log: secret now redacted.
	logger.Info("after", "path", "/bot"+"late-registered-token"+"/send")
	assert.NotContains(t, buf.String(), "late-registered-token")
	assert.Contains(t, buf.String(), "[REDACTED]")
}
