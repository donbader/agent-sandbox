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

// Handler wraps a slog.Handler and scans all attribute values for registered secrets.
type Handler struct {
	inner   slog.Handler
	secrets []string // non-empty secret values to scan for
}

// NewHandler creates a redacting handler that wraps inner.
// Any log attribute whose string representation contains one of the provided
// secret values will have that value replaced with [REDACTED].
// Empty strings in secrets are ignored.
func NewHandler(inner slog.Handler, secrets []string) *Handler {
	// Filter out empty strings — they'd match everything.
	filtered := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	return &Handler{inner: inner, secrets: filtered}
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	// Redact the message itself.
	r.Message = h.redact(r.Message)

	// Rebuild attrs with redacted values.
	var redacted []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		redacted = append(redacted, h.redactAttr(a))
		return true
	})

	// Create a new record with the redacted attrs.
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	nr.AddAttrs(redacted...)
	return h.inner.Handle(ctx, nr)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.redactAttr(a)
	}
	return &Handler{inner: h.inner.WithAttrs(redacted), secrets: h.secrets}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{inner: h.inner.WithGroup(name), secrets: h.secrets}
}

// redactAttr recursively redacts an attribute's value.
func (h *Handler) redactAttr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.redact(a.Value.String()))
	case slog.KindGroup:
		attrs := a.Value.Group()
		redacted := make([]slog.Attr, len(attrs))
		for i, ga := range attrs {
			redacted[i] = h.redactAttr(ga)
		}
		a.Value = slog.GroupValue(redacted...)
	case slog.KindAny:
		// For arbitrary values, redact the string representation if it contains a secret.
		str := a.Value.String()
		replaced := h.redact(str)
		if replaced != str {
			a.Value = slog.StringValue(replaced)
		}
	}
	return a
}

// redact replaces all occurrences of known secrets in s with the placeholder.
func (h *Handler) redact(s string) string {
	for _, secret := range h.secrets {
		if strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, placeholder)
		}
	}
	return s
}
