// Package proxy implements transparent proxies for the gateway.
// http_proxy.go provides a transparent HTTP reverse proxy that intercepts
// plain HTTP requests redirected via iptables, applies rewriters (auth-header
// injection), and forwards upstream.
package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/donbader/agent-sandbox/gateway/internal/mitm"
)

// HTTPProxy is a transparent HTTP reverse proxy that intercepts plain HTTP
// traffic redirected via iptables, applies rewriters, and forwards upstream.
type HTTPProxy struct {
	listenAddr string
	domains    []string
	rewriters  []mitm.Rewriter
}

// NewHTTPProxy creates a new HTTP proxy that intercepts requests for the given
// domains and applies rewriters before forwarding.
func NewHTTPProxy(listenAddr string, domains []string, rewriters []mitm.Rewriter) *HTTPProxy {
	return &HTTPProxy{
		listenAddr: listenAddr,
		domains:    domains,
		rewriters:  rewriters,
	}
}

// ListenAndServe starts the HTTP proxy listener.
func (h *HTTPProxy) ListenAndServe() error {
	server := &http.Server{
		Addr:         h.listenAddr,
		Handler:      h,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	return server.ListenAndServe()
}

// ServeHTTP handles each proxied HTTP request.
func (h *HTTPProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	bareHost := host
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		bareHost = hostname
	}

	if !h.matchesDomain(bareHost) {
		slog.Debug("http proxy: domain not matched, passing through", "host", host)
	}

	// Apply rewriters
	rewritten := false
	for _, rw := range h.rewriters {
		if rw.RewriteRequest(req) {
			rewritten = true
		}
	}
	slog.Debug("http proxy request", "host", host, "method", req.Method, "path", req.URL.Path, "rewritten", rewritten)

	// Determine upstream target — use the original Host header (includes port)
	target := host
	if _, _, err := net.SplitHostPort(target); err != nil {
		// No port specified, default to 80
		target = net.JoinHostPort(target, "80")
	}

	// Forward via reverse proxy
	proxy := &httputil.ReverseProxy{
		Director: func(outReq *http.Request) {
			outReq.URL.Scheme = "http"
			outReq.URL.Host = target
			outReq.Host = req.Host
		},
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 10,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("http proxy upstream error", "host", host, "error", err)
			http.Error(w, fmt.Sprintf("gateway: upstream error: %v", err), http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, req)
}

// matchesDomain checks if the host is in the configured HTTP domain list.
func (h *HTTPProxy) matchesDomain(host string) bool {
	for _, d := range h.domains {
		dHost := d
		if hostname, _, err := net.SplitHostPort(d); err == nil {
			dHost = hostname
		}
		if dHost == host {
			return true
		}
	}
	return false
}
