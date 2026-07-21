// Package proxy implements a transparent TCP proxy with SNI-based routing.
package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Proxy is a transparent TCP proxy that intercepts TLS connections,
// extracts SNI, and either passes through or applies handlers.
type Proxy struct {
	config      *Config
	handlers    []RequestHandler
	httpHandler *HTTPHandler
	listener    net.Listener
	egress      *EgressFilter
	vpnDialers  map[string]*VPNDialer // keyed by profile name
	sem         chan struct{}          // connection semaphore to cap concurrency
}

// New creates a new proxy with the given config.
func New(cfg *Config) *Proxy {
	maxConns := cfg.MaxConns
	if maxConns <= 0 {
		maxConns = 1024
	}
	return &Proxy{
		config:     cfg,
		egress:     NewEgressFilter(cfg.EgressRules),
		vpnDialers: BuildVPNDialers(cfg.VPNProfiles),
		sem:        make(chan struct{}, maxConns),
	}
}

// RegisterHandler adds a request handler for credential injection.
func (p *Proxy) RegisterHandler(h RequestHandler) {
	p.handlers = append(p.handlers, h)
}

// RegisterHTTPHandler sets the HTTP proxy handler for plain HTTP traffic.
func (p *Proxy) RegisterHTTPHandler(h *HTTPHandler) {
	p.httpHandler = h
}

// ListenAndServe starts the proxy listener.
func (p *Proxy) ListenAndServe() error {
	ln, err := net.Listen("tcp", p.config.Listen)
	if err != nil {
		return fmt.Errorf("proxy listen: %w", err)
	}
	p.listener = ln

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil // clean shutdown
			}
			// Transient errors (EMFILE, etc.) — log and retry.
			slog.Warn("proxy accept error, retrying", "error", err)
			time.Sleep(5 * time.Millisecond)
			continue
		}
		select {
		case p.sem <- struct{}{}:
		default:
			// At capacity — reject connection immediately
			_ = conn.Close()
			slog.Warn("proxy at max connections, rejecting", "remote_addr", conn.RemoteAddr())
			continue
		}
		go func() {
			defer func() { <-p.sem }()
			p.handleConn(conn)
		}()
	}
}

// Close stops the proxy.
func (p *Proxy) Close() error {
	if p.listener != nil {
		return p.listener.Close()
	}
	return nil
}

func (p *Proxy) handleConn(clientConn net.Conn) {
	defer func() { _ = clientConn.Close() }()

	slog.Debug("new connection", "remote_addr", clientConn.RemoteAddr())

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}

	// Read TLS record header (5 bytes: content_type[1] + version[2] + length[2])
	// This is also enough to detect HTTP methods ("GET ", "POST", etc.)
	header := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		slog.Debug("read initial data", "error", err)
		return
	}

	// Non-TLS: check for HTTP or block
	if header[0] != 0x16 {
		// Read more data for HTTP detection (up to 4091 additional bytes)
		extra := make([]byte, 4091)
		n, _ := clientConn.Read(extra)
		hello := append(header, extra[:n]...)
		_ = clientConn.SetReadDeadline(time.Time{})

		if isHTTP(hello) && p.httpHandler != nil {
			slog.Debug("connection detected as HTTP", "remote_addr", clientConn.RemoteAddr())
			p.httpHandler.Handle(clientConn, hello)
		} else {
			slog.Debug("unknown protocol blocked", "remote_addr", clientConn.RemoteAddr(), "first_byte", fmt.Sprintf("0x%02x", hello[0]))
		}
		return
	}

	// TLS: parse record length and read the full record (up to 16KB)
	recordLen := int(binary.BigEndian.Uint16(header[3:5]))
	if recordLen > 16384 { // TLS max record size
		slog.Debug("TLS record too large", "remote_addr", clientConn.RemoteAddr(), "length", recordLen)
		return
	}

	record := make([]byte, 5+recordLen)
	copy(record, header)
	if _, err := io.ReadFull(clientConn, record[5:]); err != nil {
		slog.Debug("read TLS record body", "error", err)
		return
	}
	_ = clientConn.SetReadDeadline(time.Time{})

	hello := record
	serverName := extractSNI(hello)
	if serverName == "" {
		slog.Debug("no SNI in connection", "remote_addr", clientConn.RemoteAddr())
		return
	}

	// Check egress rules before processing
	if p.egress.HasRules() {
		decision := p.egress.AllowHost(serverName)
		if !decision.Allowed {
			slog.Warn("egress blocked", "sni", serverName, "reason", "denied_by_policy")
			return
		}
	}

	// Check if any handler wants to intercept this host
	for _, h := range p.handlers {
		if h.Matches(serverName) {
			slog.Debug("connection matched handler", "sni", serverName, "mode", "mitm")
			h.Handle(clientConn, hello, serverName)
			return
		}
	}

	// Default: passthrough to destination
	slog.Debug("connection passthrough", "sni", serverName)
	p.passthrough(clientConn, hello, serverName)
}

// passthrough pipes the connection directly to the destination on port 443.
// If the matched egress rule specifies a VPN profile, the connection is dialled
// through that profile's proxy instead of directly.
//
// NOTE: Since iptables DNAT rewrites the destination, we lose the original port.
// This means TLS on non-443 ports will be dialed on 443 instead. This is acceptable
// because nearly all TLS traffic uses 443. Non-standard ports can be supported later
// via SO_ORIGINAL_DST or port-specific iptables rules.
func (p *Proxy) passthrough(clientConn net.Conn, initialData []byte, serverName string) {
	destAddr := net.JoinHostPort(serverName, "443")

	var serverConn net.Conn
	var dialErr error

	// Check whether this host should be routed through a VPN profile.
	if len(p.vpnDialers) > 0 {
		decision := p.egress.AllowHost(serverName)
		if decision.Rule != nil && decision.Rule.VPN != "" {
			if dialer, ok := p.vpnDialers[decision.Rule.VPN]; ok {
				slog.Debug("passthrough via vpn", "host", serverName, "vpn_profile", decision.Rule.VPN)
				serverConn, dialErr = dialer.Dial("tcp", destAddr)
			} else {
				// Profile was validated at load time, so this should never happen.
				slog.Error("vpn profile not found", "profile", decision.Rule.VPN, "host", serverName)
			}
		}
	}

	// Fall back to direct dial if no VPN applies.
	if serverConn == nil && dialErr == nil {
		serverConn, dialErr = net.DialTimeout("tcp", destAddr, 10*time.Second)
	}
	if dialErr != nil {
		slog.Error("upstream connection failed", "host", destAddr, "error", dialErr)
		return
	}
	defer func() { _ = serverConn.Close() }()

	// Send the initial ClientHello that we already read
	if _, err := serverConn.Write(initialData); err != nil {
		slog.Error("write initial data", "host", destAddr, "error", err)
		return
	}

	// Bidirectional pipe
	pipe(clientConn, serverConn)
}

// isHTTP checks if the initial bytes look like an HTTP request method.
func isHTTP(data []byte) bool {
	methods := []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "PATCH ", "CONNECT "}
	for _, m := range methods {
		if len(data) >= len(m) && string(data[:len(m)]) == m {
			return true
		}
	}
	return false
}

// pipe copies data bidirectionally between two connections.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		if tc, ok := b.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		if tc, ok := a.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	wg.Wait()
}
