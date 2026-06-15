// Package redact provides a slog.Handler that replaces known secret values
// in log output with a redaction placeholder. This acts as a safety net:
// even if a secret appears in an unexpected field (URL path, error message, etc.),
// it will be masked before reaching the log output.
package redact

import (
	"context"
	"log/slog"
	"strings"
)

const placeholder = "[REDACTED]"

// SecretsFunc returns the current set of secrets to redact.
// It is called on every log record to pick up dynamically registered secrets.
type SecretsFunc func() []string

// Handler wraps a slog.Handler and scans all attribute values for registered secrets.
type Handler struct {
	inner      slog.Handler
	secretsFn  SecretsFunc // live source of secrets
	baseSecret []string    // static secrets provided at construction (optimization)
}

// NewHandler creates a redacting handler that wraps inner.
// Any log attribute whose string representation contains one of the provided
// secret values will have that value replaced with [REDACTED].
// Empty strings in secrets are ignored.
//
// For secrets registered dynamically after startup (e.g. by TS plugins),
// use WithSecretsFunc instead.
func NewHandler(inner slog.Handler, secrets []string) *Handler {
	// Filter out empty strings — they'd match everything.
	filtered := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	return &Handler{inner: inner, baseSecret: filtered}
}

// WithSecretsFunc returns a copy of the handler that also consults fn on every
// log record for dynamically registered secrets. The static secrets from
// NewHandler are still applied.
func (h *Handler) WithSecretsFunc(fn SecretsFunc) *Handler {
	return &Handler{inner: h.inner, secretsFn: fn, baseSecret: h.baseSecret}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	secrets := h.secrets()

	// Redact the message itself.
	r.Message = redact(r.Message, secrets)

	// Rebuild attrs with redacted values.
	var redacted []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		redacted = append(redacted, redactAttr(a, secrets))
		return true
	})

	// Create a new record with the redacted attrs.
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	nr.AddAttrs(redacted...)
	return h.inner.Handle(ctx, nr)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	secrets := h.secrets()
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a, secrets)
	}
	return &Handler{inner: h.inner.WithAttrs(redacted), secretsFn: h.secretsFn, baseSecret: h.baseSecret}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), secretsFn: h.secretsFn, baseSecret: h.baseSecret}
}

// secrets returns the combined set of base + dynamic secrets for redaction.
func (h *Handler) secrets() []string {
	if h.secretsFn == nil {
		return h.baseSecret
	}
	dynamic := h.secretsFn()
	if len(h.baseSecret) == 0 {
		return dynamic
	}
	if len(dynamic) == 0 {
		return h.baseSecret
	}
	combined := make([]string, 0, len(h.baseSecret)+len(dynamic))
	combined = append(combined, h.baseSecret...)
	combined = append(combined, dynamic...)
	return combined
}

// redactAttr recursively redacts an attribute's value.
func redactAttr(a slog.Attr, secrets []string) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(redact(a.Value.String(), secrets))
	case slog.KindGroup:
		attrs := a.Value.Group()
		redacted := make([]slog.Attr, len(attrs))
		for i, ga := range attrs {
			redacted[i] = redactAttr(ga, secrets)
		}
		a.Value = slog.GroupValue(redacted...)
	case slog.KindAny:
		// For arbitrary values, redact the string representation if it contains a secret.
		str := a.Value.String()
		replaced := redact(str, secrets)
		if replaced != str {
			a.Value = slog.StringValue(replaced)
		}
	}
	return a
}

// redact replaces all occurrences of known secrets in s with the placeholder.
func redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, placeholder)
		}
	}
	return s
}
