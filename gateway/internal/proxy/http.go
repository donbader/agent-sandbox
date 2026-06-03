package proxy

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// HTTPHandler proxies plain HTTP requests, applying rewriters for header injection.
type HTTPHandler struct {
	services  map[string]string // host → host:port mapping
	rewriters []HTTPRewriter
}

// HTTPRewriter modifies HTTP requests before forwarding (same interface as mitm.Rewriter).
type HTTPRewriter interface {
	RewriteRequest(req *http.Request) bool
}

// NewHTTPHandler creates an HTTP proxy handler for the given services.
func NewHTTPHandler(services []HTTPService, rewriters []HTTPRewriter) *HTTPHandler {
	svcMap := make(map[string]string, len(services))
	for _, s := range services {
		port := s.Port
		if port == "" {
			port = "80"
		}
		svcMap[s.Host] = net.JoinHostPort(s.Host, port)
	}
	return &HTTPHandler{
		services:  svcMap,
		rewriters: rewriters,
	}
}

// Handle processes an HTTP connection: reads requests in a loop (keep-alive),
// applies rewriters, and forwards to the upstream service.
func (h *HTTPHandler) Handle(clientConn net.Conn, initialData []byte) {
	// Build a reader that replays the initial data then reads from the conn
	var reader *bufio.Reader
	if len(initialData) > 0 {
		combined := io.MultiReader(
			strings.NewReader(string(initialData)),
			clientConn,
		)
		reader = bufio.NewReader(combined)
	} else {
		reader = bufio.NewReader(clientConn)
	}

	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			if err != io.EOF {
				slog.Debug("http: read request", "error", err)
			}
			return
		}

		// Determine upstream target from Host header
		host := req.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}

		target, known := h.services[host]
		if !known {
			// For unknown hosts, try to forward using the Host header as-is
			if req.Host != "" {
				if _, _, err := net.SplitHostPort(req.Host); err != nil {
					// No port specified, default to 80
					target = net.JoinHostPort(req.Host, "80")
				} else {
					target = req.Host
				}
			} else {
				slog.Debug("http: no host header, dropping", "remote", clientConn.RemoteAddr())
				sendHTTPError(clientConn, http.StatusBadRequest, "missing Host header")
				return
			}
		}

		// Apply rewriters (header injection)
		rewritten := false
		for _, rw := range h.rewriters {
			if rw.RewriteRequest(req) {
				rewritten = true
			}
		}
		slog.Debug("http: request", "host", req.Host, "method", req.Method, "path", req.URL.Path, "target", target, "rewritten", rewritten)

		// Forward to upstream
		resp, err := h.forwardHTTP(req, target)
		if err != nil {
			slog.Error("http: upstream failed", "target", target, "error", err)
			sendHTTPError(clientConn, http.StatusBadGateway, "gateway: upstream error")
			return
		}

		// Write response back to client
		if err := resp.Write(clientConn); err != nil {
			slog.Error("http: write response", "error", err)
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

// forwardHTTP sends the request to the upstream over plain HTTP.
func (h *HTTPHandler) forwardHTTP(req *http.Request, target string) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = target
	req.RequestURI = "" // must be empty for client requests

	transport := &http.Transport{
		DisableCompression: true,
	}

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return client.Do(req)
}

// sendHTTPError writes a simple HTTP error response.
func sendHTTPError(conn net.Conn, status int, msg string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(msg), msg)
	_, _ = io.WriteString(conn, resp)
}


