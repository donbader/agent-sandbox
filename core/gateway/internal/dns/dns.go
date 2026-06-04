// Package dns implements a simple DNS resolver that forwards queries upstream.
// It intercepts all DNS traffic from the agent to prevent DNS-based bypasses.
package dns

import (
	"fmt"
	"log/slog"
	"net"
)

// upstreamServers lists DNS servers to try in order.
// Docker embedded DNS resolves container names on joined networks.
// Public DNS resolves internet hostnames.
var upstreamServers = []string{"127.0.0.11:53", "8.8.8.8:53"}

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
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 4096)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			slog.Debug("read error", "error", err)
			continue
		}

		query := make([]byte, n)
		copy(query, buf[:n])

		go s.handleQuery(conn, clientAddr, query)
	}
}

func (s *Server) handleQuery(conn *net.UDPConn, clientAddr *net.UDPAddr, query []byte) {
	slog.Debug("dns query", "client", clientAddr.String(), "size", len(query))

	resp := make([]byte, 4096)

	for i, upstream := range upstreamServers {
		upConn, err := net.Dial("udp", upstream)
		if err != nil {
			slog.Debug("dns dial upstream failed", "upstream", upstream, "error", err)
			continue
		}

		if _, err := upConn.Write(query); err != nil {
			_ = upConn.Close()
			slog.Debug("dns write upstream failed", "upstream", upstream, "error", err)
			continue
		}

		n, err := upConn.Read(resp)
		_ = upConn.Close()
		if err != nil {
			slog.Debug("dns read upstream failed", "upstream", upstream, "error", err)
			continue
		}

		// If Docker DNS returned an answer, use it immediately.
		// If NXDOMAIN from Docker DNS, try next upstream (public DNS).
		hasAnswer := n > 7 && (resp[6] > 0 || resp[7] > 0)
		isLast := i == len(upstreamServers)-1

		if hasAnswer || isLast {
			if _, err := conn.WriteToUDP(resp[:n], clientAddr); err != nil {
				slog.Error("dns write client", "error", err)
			}
			return
		}
	}

	slog.Error("dns all upstreams failed", "client", clientAddr.String())
}
