// Package proxy implements a transparent TCP proxy with SNI-based routing.
package proxy

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// Proxy is a transparent TCP proxy that intercepts TLS connections,
// extracts SNI, and either passes through or applies handlers.
type Proxy struct {
	config   *Config
	handlers []RequestHandler
	listener net.Listener
}

// New creates a new proxy with the given config.
func New(cfg *Config) *Proxy {
	return &Proxy{
		config: cfg,
	}
}

// RegisterHandler adds a request handler for credential injection.
func (p *Proxy) RegisterHandler(h RequestHandler) {
	p.handlers = append(p.handlers, h)
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
			return fmt.Errorf("proxy accept: %w", err)
		}
		go p.handleConn(conn)
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
	defer clientConn.Close()

	// Read the TLS ClientHello to extract SNI
	clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, err := clientConn.Read(buf)
	if err != nil {
		log.Printf("gateway: read client hello: %v", err)
		return
	}
	clientConn.SetReadDeadline(time.Time{})

	hello := buf[:n]
	serverName := extractSNI(hello)
	if serverName == "" {
		log.Printf("gateway: no SNI in connection from %s", clientConn.RemoteAddr())
		return
	}

	// Check if any handler wants to intercept this host
	for _, h := range p.handlers {
		if h.Matches(serverName) {
			h.Handle(clientConn, hello, serverName)
			return
		}
	}

	// Default: passthrough to destination
	p.passthrough(clientConn, hello, serverName)
}

// passthrough pipes the connection directly to the destination.
func (p *Proxy) passthrough(clientConn net.Conn, initialData []byte, serverName string) {
	destAddr := net.JoinHostPort(serverName, "443")
	serverConn, err := net.DialTimeout("tcp", destAddr, 10*time.Second)
	if err != nil {
		log.Printf("gateway: dial %s: %v", destAddr, err)
		return
	}
	defer serverConn.Close()

	// Send the initial ClientHello that we already read
	if _, err := serverConn.Write(initialData); err != nil {
		log.Printf("gateway: write initial data to %s: %v", destAddr, err)
		return
	}

	// Bidirectional pipe
	pipe(clientConn, serverConn)
}

// pipe copies data bidirectionally between two connections.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(b, a)
		b.(*net.TCPConn).CloseWrite()
	}()

	go func() {
		defer wg.Done()
		io.Copy(a, b)
		a.(*net.TCPConn).CloseWrite()
	}()

	wg.Wait()
}
