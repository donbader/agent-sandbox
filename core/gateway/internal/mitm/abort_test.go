package mitm

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

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

// TestHandle_AbortResponseFraming verifies that when middleware calls ctx.abort(),
// the MITM handler writes a properly framed HTTP response that clients can read
// without hanging. This covers Content-Length, Content-Type, Connection: close,
// and connection termination.
func TestHandle_AbortResponseFraming(t *testing.T) {
	gateway.ResetForTesting()

	gateway.RegisterMiddleware("abort-framing", func(ctx *gateway.MiddlewareContext) error {
		ctx.Abort(http.StatusUnauthorized, `{"error":"oauth_required","provider":"slack"}`)
		return nil
	})
	gateway.BindDomains("abort-framing", []string{"mcp.slack.com"})

	ca := testCA(t)
	handler := NewHandler([]string{"mcp.slack.com"}, ca)

	// Create a pipe to simulate the client-server connection
	clientRaw, serverRaw := net.Pipe()
	defer clientRaw.Close() //nolint:errcheck

	// Build TLS config for client that trusts our test CA
	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	require.NoError(t, err)
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	clientTLSCfg := &tls.Config{
		ServerName: "mcp.slack.com",
		RootCAs:    caPool,
	}

	// Start the MITM handler in a goroutine (it reads from serverRaw)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The handler expects the raw connection with initial TLS ClientHello data.
		// We simulate this by doing TLS on both sides: client does tls.Client,
		// handler does its own TLS server via Handle().
		// But Handle() expects to read the initial TLS data itself, so we need
		// to pass the raw connection directly.
		handler.Handle(serverRaw, nil, "mcp.slack.com")
	}()

	// Client side: do TLS handshake directly (handler does tls.Server internally,
	// but we passed nil initialData so it reads from the conn via prefixConn)
	clientConn := tls.Client(clientRaw, clientTLSCfg)
	defer clientConn.Close() //nolint:errcheck

	err = clientConn.Handshake()
	require.NoError(t, err)

	// Send an HTTP request
	reqStr := "GET /.well-known/oauth-protected-resource HTTP/1.1\r\nHost: mcp.slack.com\r\n\r\n"
	_, err = clientConn.Write([]byte(reqStr))
	require.NoError(t, err)

	// Read the response with a timeout to catch hangs
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	require.NoError(t, err, "response should be readable without hanging")
	defer resp.Body.Close() //nolint:errcheck

	// Verify response framing
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"),
		"should default Content-Type to application/json when middleware doesn't set it")
	assert.True(t, resp.Close,
		"should signal connection close to terminate connection")
	assert.Equal(t, int64(45), resp.ContentLength,
		"should set Content-Length matching body length")

	// Read body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"error":"oauth_required","provider":"slack"}`, string(body))

	// Verify connection is closed after response (not kept alive)
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	_, err = clientConn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF, "connection should be closed after abort response")

	// Wait for handler goroutine to finish
	<-done
}

// TestHandle_AbortResponseWithCustomContentType verifies middleware-set
// Content-Type is preserved (not overwritten with application/json).
func TestHandle_AbortResponseWithCustomContentType(t *testing.T) {
	gateway.ResetForTesting()

	gateway.RegisterMiddleware("abort-custom-ct", func(ctx *gateway.MiddlewareContext) error {
		ctx.Abort(http.StatusForbidden, "access denied")
		ctx.SetAbortHeader("Content-Type", "text/plain")
		return nil
	})
	gateway.BindDomains("abort-custom-ct", []string{"api.example.com"})

	ca := testCA(t)
	handler := NewHandler([]string{"api.example.com"}, ca)

	clientRaw, serverRaw := net.Pipe()
	defer clientRaw.Close() //nolint:errcheck

	caCert, err := x509.ParseCertificate(ca.Certificate[0])
	require.NoError(t, err)
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.Handle(serverRaw, nil, "api.example.com")
	}()

	clientConn := tls.Client(clientRaw, &tls.Config{
		ServerName: "api.example.com",
		RootCAs:    caPool,
	})
	defer clientConn.Close() //nolint:errcheck

	err = clientConn.Handshake()
	require.NoError(t, err)

	_, err = clientConn.Write([]byte("GET /secret HTTP/1.1\r\nHost: api.example.com\r\n\r\n"))
	require.NoError(t, err)

	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp, err := http.ReadResponse(bufio.NewReader(clientConn), nil)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"),
		"should preserve middleware-set Content-Type")
	assert.Equal(t, int64(13), resp.ContentLength)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "access denied", string(body))

	// Verify connection is closed after response
	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	_, err = clientConn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF, "connection should be closed after abort response")

	<-done
}
