// Package mitm implements TLS man-in-the-middle interception for the gateway.
// It terminates TLS for configured domains using a sandbox CA, parses HTTP,
// applies middleware (e.g., credential injection), and forwards to the real server.
package mitm

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/donbader/agent-sandbox/core/sdk/gateway"
)

// Handler implements proxy.RequestHandler for MITM domains.
// It terminates TLS using the sandbox CA, parses HTTP requests,
// applies middleware, and forwards to the real destination.
type Handler struct {
	domains          []string
	caCert           tls.Certificate
	certCache        *CertCache
	transportCache   sync.Map // keyed by serverName → *http.Transport
	insecureUpstream bool     // read once at startup from GATEWAY_INSECURE_UPSTREAM

	// DenyPathChecker is an optional callback for egress path filtering.
	// Returns true if the request should be blocked.
	// Set by the gateway main.go wiring after construction.
	DenyPathChecker func(host, method, path string) bool

	// DenyGraphQLChecker is an optional callback for GraphQL mutation blocking.
	// Returns true if the request should be blocked.
	// Set by the gateway main.go wiring after construction.
	DenyGraphQLChecker func(host string, req *http.Request) bool

	// VPNDialFunc returns a context-aware dial function for the given server name, or nil
	// to use the default direct TCP dial. The returned function matches http.Transport.DialContext
	// so it can be assigned directly. Used to route MITM upstream connections through a VPN
	// proxy for hosts that match an egress rule with vpn: set.
	// Set by the gateway main.go wiring after construction.
	VPNDialFunc func(serverName string) func(context.Context, string, string) (net.Conn, error)
}

// NewHandler creates a MITM handler for the given domains.
func NewHandler(domains []string, caCert tls.Certificate) *Handler {
	return &Handler{
		domains:          domains,
		caCert:           caCert,
		certCache:        NewCertCache(),
		insecureUpstream: os.Getenv("GATEWAY_INSECURE_UPSTREAM") == "true",
	}
}

// Matches returns true if the host is in the MITM domain list.
func (h *Handler) Matches(host string) bool {
	for _, d := range h.domains {
		if host == d {
			return true
		}
	}
	return false
}

// Handle terminates TLS, parses HTTP, applies middleware, and forwards.
func (h *Handler) Handle(clientConn net.Conn, initialData []byte, serverName string) {
	// Generate a cert for this domain signed by our CA
	cert, err := h.certCache.GetOrCreate(serverName, h.caCert)
	if err != nil {
		slog.Error("generate cert", "host", serverName, "error", err)
		return
	}

	// Create a TLS server using the generated cert
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	// We need to replay the initial ClientHello to the TLS server.
	prefixedConn := &prefixConn{
		Conn:   clientConn,
		prefix: initialData,
	}

	tlsConn := tls.Server(prefixedConn, tlsCfg)
	defer func() { _ = tlsConn.Close() }()

	if err := tlsConn.Handshake(); err != nil {
		slog.Debug("tls handshake", "host", serverName, "error", err)
		return
	}

	slog.Debug("tls handshake complete", "host", serverName)

	// Read HTTP request from the decrypted stream
	reader := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("read request", "host", serverName, "error", err)
			}
			return
		}

		// Log the request BEFORE rewriting to avoid leaking injected secrets.
		originalPath := req.URL.Path
		contentLength := req.ContentLength

		// Check deny_paths before processing
		if h.DenyPathChecker != nil && h.DenyPathChecker(serverName, req.Method, originalPath) {
			slog.Warn("mitm: path blocked", "host", serverName, "method", req.Method, "path", originalPath)
			blockResp := &http.Response{
				StatusCode: http.StatusForbidden,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("path blocked by policy")),
			}
			_ = blockResp.Write(tlsConn)
			continue
		}

		// Check deny_graphql before processing
		if h.DenyGraphQLChecker != nil && h.DenyGraphQLChecker(serverName, req) {
			blockResp := &http.Response{
				StatusCode: http.StatusForbidden,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("graphql mutation blocked by policy")),
			}
			_ = blockResp.Write(tlsConn)
			continue
		}

		ctx, rewritten := applyMiddlewareWithContext(req)
		slog.Debug("request", "host", serverName, "method", req.Method, "path", originalPath, "rewritten", rewritten, "content_length", contentLength)

		// If middleware aborted, return the abort response instead of forwarding
		if ctx != nil && ctx.AbortStatus != 0 {
			abortResp := &http.Response{
				StatusCode:    ctx.AbortStatus,
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        ctx.AbortHeaders,
				Body:          io.NopCloser(strings.NewReader(ctx.AbortBody)),
				ContentLength: int64(len(ctx.AbortBody)),
				Close:         true,
			}
			if abortResp.Header == nil {
				abortResp.Header = make(http.Header)
			}
			if abortResp.Header.Get("Content-Type") == "" {
				abortResp.Header.Set("Content-Type", "application/json")
			}
			_ = abortResp.Write(tlsConn)
			return
		}

		// Forward to real server
		resp, err := h.forwardRequest(req, serverName)
		if err != nil {
			slog.Error("upstream connection failed", "host", serverName, "method", req.Method, "path", req.URL.Path, "error", err)
			errResp := &http.Response{
				StatusCode: http.StatusBadGateway,
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("gateway: upstream error")),
			}
			_ = errResp.Write(tlsConn)
			return
		}

		// Write response back to client
		contentType := resp.Header.Get("Content-Type")
		transferEncoding := resp.Header.Get("Transfer-Encoding")
		slog.Debug("forwarding response", "host", serverName, "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "content_type", contentType, "transfer_encoding", transferEncoding, "content_length", resp.ContentLength)
		writeStart := time.Now()

		// For streaming responses (SSE/chunked), write headers first then copy body
		// incrementally. This avoids blocking on resp.Write() which waits for the
		// entire body before returning — problematic for long-lived SSE streams.
		if strings.Contains(contentType, "text/event-stream") || transferEncoding == "chunked" {
			if err := writeResponseHeaders(tlsConn, resp); err != nil {
				slog.Debug("stream headers write failed", "host", serverName, "path", req.URL.Path, "error", err)
				_ = resp.Body.Close()
				return
			}
			// Stream body — broken pipe here is expected (client got what it needed)
			_, err := io.Copy(tlsConn, resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				slog.Debug("stream ended", "host", serverName, "method", req.Method, "path", req.URL.Path, "duration_ms", time.Since(writeStart).Milliseconds(), "error", err)
			} else {
				slog.Debug("stream complete", "host", serverName, "method", req.Method, "path", req.URL.Path, "duration_ms", time.Since(writeStart).Milliseconds())
			}
			return // SSE connections are not reused
		}

		if err := resp.Write(tlsConn); err != nil {
			slog.Error("write response", "host", serverName, "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "content_type", contentType, "duration_ms", time.Since(writeStart).Milliseconds(), "error", err)
			_ = resp.Body.Close()
			return
		}
		_ = resp.Body.Close()
		slog.Debug("response complete", "host", serverName, "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "content_type", contentType, "duration_ms", time.Since(writeStart).Milliseconds())

		// Check if connection should be kept alive
		if req.Close || resp.Close {
			return
		}
	}
}

// scopedEnv returns an env lookup function restricted to GATEWAY_* and PLUGIN_* prefixed
// variables. This prevents plugins from reading arbitrary process environment (Docker secrets, etc.).
func scopedEnv(key string) string {
	if strings.HasPrefix(key, "GATEWAY_") || strings.HasPrefix(key, "PLUGIN_") {
		return os.Getenv(key)
	}
	return ""
}

// applyMiddlewareWithContext runs middleware and returns the context and whether any matched.
// If ctx.AbortStatus is non-zero, the request should be aborted (return a response without forwarding).
func applyMiddlewareWithContext(req *http.Request) (*gateway.MiddlewareContext, bool) {
	matching := gateway.MatchingMiddleware(req)
	if len(matching) == 0 {
		return nil, false
	}

	ctx := &gateway.MiddlewareContext{
		Request: req,
		Env:     scopedEnv,
	}

	for _, mw := range matching {
		ctx.Domain = gateway.MatchedDomain(mw, req)
		if err := mw.Func(ctx); err != nil {
			slog.Error("middleware error", "name", mw.Name, "error", err)
			continue
		}
		if ctx.AbortStatus != 0 {
			return ctx, true
		}
	}
	return ctx, true
}

// getTransport returns a cached *http.Transport for the given serverName, creating
// one on first use. Reusing transports enables TCP/TLS connection pooling.
// If a VPNDialFunc is configured and returns a dialer for this serverName, the
// transport will use it to route upstream connections through the VPN proxy.
func (h *Handler) getTransport(serverName string) *http.Transport {
	if v, ok := h.transportCache.Load(serverName); ok {
		t, _ := v.(*http.Transport)
		return t
	}

	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: h.insecureUpstream, //nolint:gosec // test-only
		},
		DisableCompression: true,
	}

	// Wire VPN dialer if this host's egress rule specifies a VPN profile.
	if h.VPNDialFunc != nil {
		if dialFn := h.VPNDialFunc(serverName); dialFn != nil {
			t.DialContext = dialFn // matches http.Transport.DialContext signature exactly
			slog.Debug("mitm: upstream dial via vpn", "host", serverName)
		}
	}

	actual, _ := h.transportCache.LoadOrStore(serverName, t)
	result, _ := actual.(*http.Transport)
	return result
}

// forwardRequest sends the request to the real server over TLS.
func (h *Handler) forwardRequest(req *http.Request, serverName string) (*http.Response, error) {
	req.URL.Host = serverName
	req.RequestURI = "" // must be empty for client requests

	if h.insecureUpstream {
		req.URL.Scheme = "http"
	} else {
		req.URL.Scheme = "https"
	}

	client := &http.Client{
		Transport: h.getTransport(serverName),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return client.Do(req)
}

// writeResponseHeaders writes the HTTP status line and headers to the connection.
func writeResponseHeaders(conn net.Conn, resp *http.Response) error {
	statusLine := fmt.Sprintf("HTTP/%d.%d %d %s\r\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, http.StatusText(resp.StatusCode))
	if _, err := io.WriteString(conn, statusLine); err != nil {
		return err
	}
	if err := resp.Header.Write(conn); err != nil {
		return err
	}
	// End of headers
	_, err := io.WriteString(conn, "\r\n")
	return err
}

// prefixConn wraps a net.Conn and prepends buffered data before reading from the real conn.
type prefixConn struct {
	net.Conn
	prefix []byte
	once   sync.Once
	reader io.Reader
}

func (c *prefixConn) Read(b []byte) (int, error) {
	c.once.Do(func() {
		c.reader = io.MultiReader(
			strings.NewReader(string(c.prefix)),
			c.Conn,
		)
	})
	return c.reader.Read(b)
}
