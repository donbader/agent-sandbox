// Package dns implements a simple DNS resolver that forwards queries upstream.
// It intercepts all DNS traffic from the agent to prevent DNS-based bypasses.
package dns

import (
	"fmt"
	"log/slog"
	"net"
)

const upstreamDNS = "8.8.8.8:53"

// Server is a UDP DNS forwarder.
type Server struct {
	listen string
}

// NewServer creates a DNS server listening on the given address.
func NewServer(listen string) *Server {
	return &Server{listen: listen}
}

// ListenAndServe starts the DNS server.
func (s *Server) ListenAndServe() error {
	addr, err := net.ResolveUDPAddr("udp", s.listen)
	if err != nil {
		return fmt.Errorf("dns resolve addr: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("dns listen: %w", err)
	}
	defer conn.Close()

	buf := make([]byte, 4096)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			slog.Debug("read error", "error", err)
			continue
		}

		go s.handleQuery(conn, clientAddr, buf[:n])
	}
}

func (s *Server) handleQuery(conn *net.UDPConn, clientAddr *net.UDPAddr, query []byte) {
	// Forward to upstream DNS
	upstream, err := net.Dial("udp", upstreamDNS)
	if err != nil {
		slog.Error("dial upstream", "error", err)
		return
	}
	defer upstream.Close()

	if _, err := upstream.Write(query); err != nil {
		slog.Error("write upstream", "error", err)
		return
	}

	resp := make([]byte, 4096)
	n, err := upstream.Read(resp)
	if err != nil {
		slog.Error("read upstream", "error", err)
		return
	}

	// Send response back to client
	if _, err := conn.WriteToUDP(resp[:n], clientAddr); err != nil {
		slog.Error("write client", "error", err)
	}
}
