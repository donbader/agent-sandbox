package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Forwarder listens on a local port and forwards TCP connections to a target.
type Forwarder struct {
	listen   string
	target   string
	listener net.Listener
}

// NewForwarder creates a TCP forwarder.
func NewForwarder(listen, target string) *Forwarder {
	return &Forwarder{listen: listen, target: target}
}

// ListenAndServe starts accepting connections and forwarding them.
func (f *Forwarder) ListenAndServe() error {
	ln, err := net.Listen("tcp", f.listen)
	if err != nil {
		return fmt.Errorf("forward listen %s: %w", f.listen, err)
	}
	f.listener = ln
	slog.Info("port forward listening", "listen", f.listen, "target", f.target)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("forward accept %s: %w", f.listen, err)
		}
		go f.handle(conn)
	}
}

// Close stops the forwarder.
func (f *Forwarder) Close() error {
	if f.listener != nil {
		return f.listener.Close()
	}
	return nil
}

func (f *Forwarder) handle(clientConn net.Conn) {
	defer clientConn.Close()

	serverConn, err := net.DialTimeout("tcp", f.target, 10*time.Second)
	if err != nil {
		slog.Debug("forward dial failed", "target", f.target, "error", err)
		return
	}
	defer serverConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(serverConn, clientConn)
		if tc, ok := serverConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(clientConn, serverConn)
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	wg.Wait()
}
