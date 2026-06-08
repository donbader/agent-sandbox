package gateway

import (
	"net/http"
	"testing"
)

func TestRegisterSecret(t *testing.T) {
	// Reset state
	secrets = nil

	RegisterSecret("my-secret-token")
	RegisterSecret("") // empty should be ignored
	RegisterSecret("another-secret")

	got := Secrets()
	if len(got) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(got))
	}
	if got[0] != "my-secret-token" {
		t.Errorf("expected 'my-secret-token', got %q", got[0])
	}
	if got[1] != "another-secret" {
		t.Errorf("expected 'another-secret', got %q", got[1])
	}
}

func TestRegisterRoute(t *testing.T) {
	ResetForTesting()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	RegisterRoute(RouteDef{Path: "/plugins/mcp-oauth/callback", Handler: handler})
	RegisterRoute(RouteDef{Path: "/plugins/other/webhook", Handler: handler})

	routes := Routes()
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0].Path != "/plugins/mcp-oauth/callback" {
		t.Errorf("expected '/plugins/mcp-oauth/callback', got %q", routes[0].Path)
	}
	if routes[1].Path != "/plugins/other/webhook" {
		t.Errorf("expected '/plugins/other/webhook', got %q", routes[1].Path)
	}
}

func TestMatchRoute(t *testing.T) {
	ResetForTesting()

	RegisterRoute(RouteDef{
		Path:    "/plugins/mcp-oauth/callback",
		Handler: func(w http.ResponseWriter, r *http.Request) {},
	})

	// Exact match
	h := MatchRoute("/plugins/mcp-oauth/callback")
	if h == nil {
		t.Fatal("expected handler for exact path match")
	}

	// Prefix match (sub-path)
	h = MatchRoute("/plugins/mcp-oauth/callback/extra")
	if h == nil {
		t.Fatal("expected handler for prefix path match")
	}

	// No match
	h = MatchRoute("/plugins/other/callback")
	if h != nil {
		t.Fatal("expected nil for non-matching path")
	}

	// No match (partial prefix without /)
	h = MatchRoute("/plugins/mcp-oauth/callbackextra")
	if h != nil {
		t.Fatal("expected nil for partial prefix without /")
	}
}

func TestMiddlewareContext_Abort(t *testing.T) {
	ctx := &MiddlewareContext{
		Request: &http.Request{},
		Env:     func(s string) string { return "" },
	}

	ctx.SetAbortHeader("X-Test", "value")
	ctx.Abort(401, `{"error":"unauthorized"}`)

	if ctx.AbortStatus != 401 {
		t.Errorf("expected status 401, got %d", ctx.AbortStatus)
	}
	if ctx.AbortBody != `{"error":"unauthorized"}` {
		t.Errorf("unexpected body: %s", ctx.AbortBody)
	}
	if ctx.AbortHeaders.Get("X-Test") != "value" {
		t.Errorf("expected X-Test header, got %q", ctx.AbortHeaders.Get("X-Test"))
	}
}
