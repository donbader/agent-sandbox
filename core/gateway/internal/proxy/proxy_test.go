package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testHandler implements RequestHandler for testing.
type testHandler struct {
	domains []string
	handled chan string
}

func (h *testHandler) Matches(host string) bool {
	for _, d := range h.domains {
		if d == host {
			return true
		}
	}
	return false
}

func (h *testHandler) Handle(conn net.Conn, _ []byte, sni string) {
	h.handled <- sni
	_ = conn.Close()
}

// pipeConn wraps net.Conn from net.Pipe to support SetReadDeadline (no-op).
type pipeConn struct {
	net.Conn
}

func (c *pipeConn) SetReadDeadline(_ time.Time) error { return nil }

func TestProxy_HTTPDetection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	upAddr := strings.TrimPrefix(upstream.URL, "http://")
	host, port, _ := net.SplitHostPort(upAddr)

	handler := NewHTTPHandler([]HTTPService{{Host: host, Port: port}})
	p := New(&Config{Listen: "127.0.0.1:0"})
	p.RegisterHTTPHandler(handler)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.handleConn(&pipeConn{server})
	}()

	req := "GET / HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
	_, err := client.Write([]byte(req))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
	_ = client.Close()
	wg.Wait()
}

func TestProxy_TLSDetection_HandlerMatch(t *testing.T) {
	p := New(&Config{Listen: "127.0.0.1:0"})
	handler := &testHandler{
		domains: []string{"api.example.com"},
		handled: make(chan string, 1),
	}
	p.RegisterHandler(handler)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	go p.handleConn(&pipeConn{server})

	hello := buildClientHello("api.example.com")
	_, err := client.Write(hello)
	require.NoError(t, err)

	select {
	case sni := <-handler.handled:
		assert.Equal(t, "api.example.com", sni)
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not called within timeout")
	}
}

func TestProxy_TLSDetection_NoSNI(t *testing.T) {
	p := New(&Config{Listen: "127.0.0.1:0"})
	handler := &testHandler{
		domains: []string{"anything.com"},
		handled: make(chan string, 1),
	}
	p.RegisterHandler(handler)

	client, server := net.Pipe()

	done := make(chan struct{})
	go func() {
		p.handleConn(&pipeConn{server})
		close(done)
	}()

	data := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x00}
	_, err := client.Write(data)
	require.NoError(t, err)
	_ = client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn did not return")
	}

	select {
	case sni := <-handler.handled:
		t.Fatalf("handler should not be called, got SNI=%s", sni)
	default:
	}
}

func TestProxy_NoHTTPHandler_DropsConnection(t *testing.T) {
	p := New(&Config{Listen: "127.0.0.1:0"})

	client, server := net.Pipe()

	done := make(chan struct{})
	go func() {
		p.handleConn(&pipeConn{server})
		close(done)
	}()

	_, err := client.Write([]byte("GET / HTTP/1.1\r\nHost: test.local\r\n\r\n"))
	require.NoError(t, err)
	_ = client.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn did not return")
	}
}

func TestHTTPHandler_MiddlewareApplied(t *testing.T) {
	gateway.ResetForTesting()
	defer gateway.ResetForTesting()

	gateway.RegisterMiddleware(gateway.MiddlewareDef{
		Name:    "test-injector",
		Domains: []string{"injected.local"},
		Func: func(ctx *gateway.MiddlewareContext) error {
			ctx.Request.Header.Set("X-Injected", "secret-token")
			return nil
		},
	})

	var receivedHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Injected")
	}))
	defer upstream.Close()

	upAddr := strings.TrimPrefix(upstream.URL, "http://")
	handler := NewHTTPHandler([]HTTPService{})
	handler.services["injected.local"] = upAddr

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Handle(server, nil)
	}()

	req := "GET /resource HTTP/1.1\r\nHost: injected.local\r\nConnection: close\r\n\r\n"
	_, err := client.Write([]byte(req))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	_ = client.Close()
	wg.Wait()

	assert.Equal(t, "secret-token", receivedHeader)
}

func TestHTTPHandler_UnknownHost_ForwardsWithHostHeader(t *testing.T) {
	gateway.ResetForTesting()
	defer gateway.ResetForTesting()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upAddr := strings.TrimPrefix(upstream.URL, "http://")
	handler := NewHTTPHandler([]HTTPService{})

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Handle(server, nil)
	}()

	req := "GET / HTTP/1.1\r\nHost: " + upAddr + "\r\nConnection: close\r\n\r\n"
	_, err := client.Write([]byte(req))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()
	_ = client.Close()
	wg.Wait()
}

func TestHTTPHandler_MissingHost_Returns400(t *testing.T) {
	handler := NewHTTPHandler([]HTTPService{})

	client, server := net.Pipe()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Handle(server, nil)
	}()

	req := "GET / HTTP/1.0\r\n\r\n"
	_, err := client.Write([]byte(req))
	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, _ := client.Read(buf)
	response := string(buf[:n])

	assert.Contains(t, response, "400")
	assert.Contains(t, response, "missing Host header")
	_ = client.Close()
	wg.Wait()
}

func TestForwarder_PipesData(t *testing.T) {
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = echoLn.Close() }()

	go func() {
		for {
			conn, err := echoLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()

	// Get a free port for the forwarder
	tmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fwdAddr := tmpLn.Addr().String()
	_ = tmpLn.Close()

	fwd := NewForwarder(fwdAddr, echoLn.Addr().String())
	go func() { _ = fwd.ListenAndServe() }()
	defer func() { _ = fwd.Close() }()

	// Wait for forwarder to be ready (no race — we use known address)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fwdAddr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := net.DialTimeout("tcp", fwdAddr, 2*time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	msg := "hello forwarder"
	_, err = conn.Write([]byte(msg))
	require.NoError(t, err)

	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}

	buf, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.Equal(t, msg, string(buf))
}
