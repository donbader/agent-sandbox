package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/donbader/agent-sandbox/gateway/internal/mitm"
	"github.com/donbader/agent-sandbox/gateway/internal/proxy"
)

func TestHTTPProxy_ForwardsRequestAndAppliesRewriter(t *testing.T) {
	// Upstream server that echoes back the injected header
	var receivedHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Test-Token")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	// Extract host:port from upstream URL
	upstreamHost := upstream.Listener.Addr().String()

	rewriter := &alwaysRewriter{
		header: "X-Test-Token",
		value:  "secret-123",
	}

	hp := proxy.NewHTTPProxy(":0", []string{upstreamHost}, []mitm.Rewriter{rewriter})

	// Use httptest to test the handler directly
	req := httptest.NewRequest(http.MethodGet, "http://"+upstreamHost+"/test-path", nil)
	req.Host = upstreamHost

	rec := httptest.NewRecorder()
	hp.ServeHTTP(rec, req)

	// The rewriter should have injected the header before forwarding
	if receivedHeader != "secret-123" {
		t.Errorf("expected upstream to receive header 'secret-123', got %q", receivedHeader)
	}

	resp := rec.Result()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "ok" {
		t.Errorf("expected body 'ok', got %q", string(body))
	}
}

func TestHTTPProxy_PreservesHostHeader(t *testing.T) {
	var receivedHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamHost := upstream.Listener.Addr().String()
	hp := proxy.NewHTTPProxy(":0", []string{upstreamHost}, nil)

	req := httptest.NewRequest(http.MethodGet, "http://"+upstreamHost+"/path", nil)
	req.Host = upstreamHost

	rec := httptest.NewRecorder()
	hp.ServeHTTP(rec, req)

	if receivedHost != upstreamHost {
		t.Errorf("expected Host header %q, got %q", upstreamHost, receivedHost)
	}
}

func TestHTTPProxy_UpstreamError(t *testing.T) {
	// Point at an address that refuses connections
	hp := proxy.NewHTTPProxy(":0", []string{"127.0.0.1:1"}, nil)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/path", nil)
	req.Host = "127.0.0.1:1"

	rec := httptest.NewRecorder()
	hp.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", resp.StatusCode)
	}
}

// alwaysRewriter injects a header on every request regardless of domain.
type alwaysRewriter struct {
	header string
	value  string
}

func (r *alwaysRewriter) RewriteRequest(req *http.Request) bool {
	req.Header.Set(r.header, r.value)
	return true
}
