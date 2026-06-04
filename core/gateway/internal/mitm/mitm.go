// Package mitm implements TLS man-in-the-middle interception for the gateway.
// It terminates TLS for configured domains using a sandbox CA, parses HTTP,
// applies URL rewriting (e.g., token replacement), and forwards to the real server.
package mitm

import (
	"bufio"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// SecretProvider is implemented by rewriters that hold sensitive values (e.g. tokens)
// that should be redacted from logs.
type SecretProvider interface {
	Secrets() []string
}

// Handler implements proxy.RequestHandler for MITM domains.
// It terminates TLS using the sandbox CA, parses HTTP requests,
// applies rewriters, and forwards to the real destination.
type Handler struct {
	domains        []string
	caCert         tls.Certificate
	certCache      *CertCache
	rewriters      []Rewriter
	transportCache sync.Map // keyed by serverName → *http.Transport
}

// Rewriter modifies HTTP requests before forwarding.
type Rewriter interface {
	// RewriteRequest modifies the request in place. Returns true if handled.
	RewriteRequest(req *http.Request) bool
}

// NewHandler creates a MITM handler for the given domains.
func NewHandler(domains []string, caCert tls.Certificate, rewriters []Rewriter) *Handler {
	return &Handler{
		domains:   domains,
		caCert:    caCert,
		certCache: NewCertCache(),
		rewriters: rewriters,
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

// Handle terminates TLS, parses HTTP, applies rewriters, and forwards.
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
	// Use a prefixed reader that first returns initialData, then reads from clientConn.
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
		rewritten := false
		for _, rw := range h.rewriters {
			if rw.RewriteRequest(req) {
				rewritten = true
			}
		}
		slog.Debug("request", "host", serverName, "method", req.Method, "path", originalPath, "rewritten", rewritten)

		// Forward to real server
		resp, err := h.forwardRequest(req, serverName)
		if err != nil {
			slog.Error("upstream connection failed", "host", serverName, "error", err)
			// Send a 502 back to client
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
		if err := resp.Write(tlsConn); err != nil {
			slog.Error("write response", "host", serverName, "error", err)
			_ = resp.Body.Close()
			return
		}
		_ = resp.Body.Close()

		// Check if connection should be kept alive
		if req.Close || resp.Close {
			return
		}
	}
}

// getTransport returns a cached *http.Transport for the given serverName, creating
// one on first use. Reusing transports enables TCP/TLS connection pooling.
func (h *Handler) getTransport(serverName string) *http.Transport {
	insecure := os.Getenv("GATEWAY_INSECURE_UPSTREAM") == "true"

	if v, ok := h.transportCache.Load(serverName); ok {
		t, _ := v.(*http.Transport)
		return t
	}

	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: insecure, //nolint:gosec // test-only
		},
		// Disable compression so we can stream the raw response bytes.
		DisableCompression: true,
	}

	// Store, but prefer an existing entry if a concurrent goroutine beat us.
	actual, _ := h.transportCache.LoadOrStore(serverName, t)
	result, _ := actual.(*http.Transport)
	return result
}

// forwardRequest sends the request to the real server over TLS.
func (h *Handler) forwardRequest(req *http.Request, serverName string) (*http.Response, error) {
	// Set the host header and request URI
	req.URL.Host = serverName
	req.RequestURI = "" // must be empty for client requests

	insecure := os.Getenv("GATEWAY_INSECURE_UPSTREAM") == "true"

	if insecure {
		// In test mode, forward as HTTP to allow plain-HTTP echo servers.
		req.URL.Scheme = "http"
	} else {
		req.URL.Scheme = "https"
	}

	client := &http.Client{
		Transport: h.getTransport(serverName),
		// Don't follow redirects — pass them through
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return client.Do(req)
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
