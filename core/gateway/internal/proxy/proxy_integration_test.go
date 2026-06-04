//go:build integration

package proxy_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/donbader/agent-sandbox/core/gateway/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxy_HandlerMatches verifies that when a handler matches the SNI,
// the connection is routed to the handler instead of passthrough.
func TestProxy_HandlerMatches(t *testing.T) {
	cfg := &proxy.Config{
		Listen:    "127.0.0.1:0",
		DNSListen: "127.0.0.1:0",
	}
	p := proxy.New(cfg)

	// Register a mock handler that records matched connections
	var mu sync.Mutex
	var handled []string
	mockHandler := &mockRequestHandler{
		matchDomains: []string{"api.example.com"},
		onHandle: func(conn net.Conn, data []byte, sni string) {
			mu.Lock()
			handled = append(handled, sni)
			mu.Unlock()
			conn.Close()
		},
	}
	p.RegisterHandler(mockHandler)

	// Start proxy
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyAddr := ln.Addr().String()
	ln.Close()

	cfg.Listen = proxyAddr
	go func() {
		_ = p.ListenAndServe()
	}()
	defer p.Close()

	waitForListener(t, proxyAddr, 2*time.Second)

	// Connect with SNI that matches our mock handler
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	require.NoError(t, err)

	// Send a TLS ClientHello with SNI = "api.example.com"
	hello := buildClientHello("api.example.com")
	_, err = conn.Write(hello)
	require.NoError(t, err)
	conn.Close()

	// Give the handler a moment to process
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, handled, 1)
	assert.Equal(t, "api.example.com", handled[0])
}

// TestProxy_Passthrough_NoHandler verifies that connections to non-matching
// SNI are passed through (not intercepted by any handler).
func TestProxy_Passthrough_NoHandler(t *testing.T) {
	cfg := &proxy.Config{
		Listen:    "127.0.0.1:0",
		DNSListen: "127.0.0.1:0",
	}
	p := proxy.New(cfg)

	// Register a handler for a different domain
	mockHandler := &mockRequestHandler{
		matchDomains: []string{"intercepted.example.com"},
		onHandle: func(conn net.Conn, data []byte, sni string) {
			t.Errorf("handler should not be called for non-matching SNI, got %s", sni)
			conn.Close()
		},
	}
	p.RegisterHandler(mockHandler)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyAddr := ln.Addr().String()
	ln.Close()

	cfg.Listen = proxyAddr
	go func() {
		_ = p.ListenAndServe()
	}()
	defer p.Close()

	waitForListener(t, proxyAddr, 2*time.Second)

	// Connect with SNI that does NOT match — proxy will try to passthrough
	// (this will fail to connect upstream since "other.example.com" doesn't resolve,
	// but the key assertion is that the handler was NOT called)
	conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
	require.NoError(t, err)

	hello := buildClientHello("other.example.com")
	_, err = conn.Write(hello)
	require.NoError(t, err)

	// Wait briefly — if handler were called, the t.Errorf above would fire
	time.Sleep(200 * time.Millisecond)
	conn.Close()
}

// --- Helpers ---

// mockRequestHandler implements proxy.RequestHandler for testing.
type mockRequestHandler struct {
	matchDomains []string
	onHandle     func(net.Conn, []byte, string)
}

func (h *mockRequestHandler) Matches(host string) bool {
	for _, d := range h.matchDomains {
		if d == host {
			return true
		}
	}
	return false
}

func (h *mockRequestHandler) Handle(conn net.Conn, data []byte, sni string) {
	h.onHandle(conn, data, sni)
}

// waitForListener waits until a TCP port is accepting connections.
func waitForListener(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener at %s did not become ready within %s", addr, timeout)
}

// buildClientHello constructs a minimal TLS 1.2 ClientHello with the given SNI.
func buildClientHello(serverName string) []byte {
	sniBytes := []byte(serverName)
	sniLen := len(sniBytes)

	// SNI extension
	sniExt := []byte{
		0x00, 0x00, // extension type: server_name
		byte((sniLen + 5) >> 8), byte((sniLen + 5) & 0xff), // extension data length
		byte((sniLen + 3) >> 8), byte((sniLen + 3) & 0xff), // SNI list length
		0x00, // host name type
		byte(sniLen >> 8), byte(sniLen & 0xff), // host name length
	}
	sniExt = append(sniExt, sniBytes...)

	extLen := len(sniExt)

	// ClientHello body
	clientHello := []byte{
		0x03, 0x03, // TLS 1.2
	}
	random := make([]byte, 32)
	clientHello = append(clientHello, random...)
	clientHello = append(clientHello, 0x00)       // session ID length = 0
	clientHello = append(clientHello, 0x00, 0x02) // cipher suites length = 2
	clientHello = append(clientHello, 0x00, 0x2f) // TLS_RSA_WITH_AES_128_CBC_SHA
	clientHello = append(clientHello, 0x01, 0x00) // compression: length=1, null
	clientHello = append(clientHello, byte(extLen>>8), byte(extLen&0xff))
	clientHello = append(clientHello, sniExt...)

	// Handshake header
	handshake := []byte{
		0x01, // ClientHello
		0x00, byte(len(clientHello) >> 8), byte(len(clientHello) & 0xff),
	}
	handshake = append(handshake, clientHello...)

	// TLS record
	record := []byte{
		0x16,       // handshake
		0x03, 0x01, // TLS 1.0 record layer
		byte(len(handshake) >> 8), byte(len(handshake) & 0xff),
	}
	record = append(record, handshake...)

	return record
}
