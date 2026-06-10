package mitm

import (
	"net/http"
	"testing"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyMiddlewareWithContext_Abort(t *testing.T) {
	gateway.ResetForTesting()

	gateway.RegisterMiddleware("test-abort", func(ctx *gateway.MiddlewareContext) error {
		ctx.Abort(http.StatusUnauthorized, `{"error":"unauthorized"}`)
		ctx.SetAbortHeader("Content-Type", "application/json")
		return nil
	})
	gateway.BindDomains("test-abort", []string{"example.com"})

	req, _ := http.NewRequest("GET", "https://example.com/api", nil)
	req.Host = "example.com"

	ctx, matched := applyMiddlewareWithContext(req)
	require.True(t, matched)
	require.NotNil(t, ctx)
	assert.Equal(t, http.StatusUnauthorized, ctx.AbortStatus)
	assert.Equal(t, `{"error":"unauthorized"}`, ctx.AbortBody)
	assert.Equal(t, "application/json", ctx.AbortHeaders.Get("Content-Type"))
}

func TestApplyMiddlewareWithContext_NoAbort(t *testing.T) {
	gateway.ResetForTesting()

	gateway.RegisterMiddleware("test-passthrough", func(ctx *gateway.MiddlewareContext) error {
		ctx.Request.Header.Set("Authorization", "Bearer token")
		return nil
	})
	gateway.BindDomains("test-passthrough", []string{"example.com"})

	req, _ := http.NewRequest("GET", "https://example.com/api", nil)
	req.Host = "example.com"

	ctx, matched := applyMiddlewareWithContext(req)
	require.True(t, matched)
	require.NotNil(t, ctx)
	assert.Equal(t, 0, ctx.AbortStatus)
	assert.Equal(t, "Bearer token", req.Header.Get("Authorization"))
}

func TestApplyMiddlewareWithContext_NoMatch(t *testing.T) {
	gateway.ResetForTesting()

	req, _ := http.NewRequest("GET", "https://unmatched.com/api", nil)
	req.Host = "unmatched.com"

	ctx, matched := applyMiddlewareWithContext(req)
	assert.False(t, matched)
	assert.Nil(t, ctx)
}
