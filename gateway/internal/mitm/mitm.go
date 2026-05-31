// Package mitm implements TLS man-in-the-middle interception for the gateway.
// It terminates TLS for configured domains using a sandbox CA, parses HTTP,
// applies URL rewriting (e.g., token replacement), and forwards to the real server.
package mitm

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Handler implements proxy.RequestHandler for MITM domains.
// It terminates TLS using the sandbox CA, parses HTTP requests,
// applies rewriters, and forwards to the real destination.
type Handler struct {
	domains   []string
	caCert    tls.Certificate
	certCache *CertCache
	rewriters []Rewriter
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
	defer tlsConn.Close()

	if err := tlsConn.Handshake(); err != nil {
		slog.Debug("tls handshake", "host", serverName, "error", err)
		return
	}

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

		// Apply rewriters
		for _, rw := range h.rewriters {
			rw.RewriteRequest(req)
		}

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
			errResp.Write(tlsConn)
			return
		}

		// Write response back to client
		if err := resp.Write(tlsConn); err != nil {
			slog.Error("write response", "host", serverName, "error", err)
			resp.Body.Close()
			return
		}
		resp.Body.Close()

		// Check if connection should be kept alive
		if req.Close || resp.Close {
			return
		}
	}
}

// forwardRequest sends the request to the real server over TLS.
func (h *Handler) forwardRequest(req *http.Request, serverName string) (*http.Response, error) {
	// Connect to real server
	destAddr := net.JoinHostPort(serverName, "443")
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	serverConn, err := tls.DialWithDialer(dialer, "tcp", destAddr, &tls.Config{
		ServerName: serverName,
	})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", destAddr, err)
	}
	defer serverConn.Close()

	// Set the host header and request URI
	req.URL.Scheme = "https"
	req.URL.Host = serverName
	req.RequestURI = "" // must be empty for client requests

	// Use http.Transport for proper request handling
	transport := &http.Transport{
		DialTLS: func(network, addr string) (net.Conn, error) {
			return serverConn, nil
		},
	}

	return transport.RoundTrip(req)
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
