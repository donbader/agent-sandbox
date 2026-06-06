// Package dns implements a simple DNS resolver that forwards queries upstream.
// It intercepts all DNS traffic from the agent to prevent DNS-based bypasses.
package dns

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
)

// upstreamServers lists DNS servers to try in order.
// Populated at startup from /etc/resolv.conf (container's embedded DNS)
// with a public DNS fallback for internet hostname resolution.
var (
	upstreamMu      sync.RWMutex
	upstreamServers = initUpstreamServers()
)

// initUpstreamServers reads nameservers from /etc/resolv.conf and appends
// a public DNS fallback. This makes the gateway work with any container runtime
// (Docker, Podman, containerd) without hardcoding runtime-specific DNS addresses.
func initUpstreamServers() []string {
	servers := parseResolvConf("/etc/resolv.conf")
	// Always add public DNS as final fallback
	servers = append(servers, "8.8.8.8:53")
	return servers
}

// parseResolvConf extracts nameserver entries from a resolv.conf file.
func parseResolvConf(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		slog.Debug("dns: could not read resolv.conf, using public DNS only", "error", err)
		return nil
	}
	defer func() { _ = f.Close() }()

	var servers []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				ip := fields[1]
				// Skip loopback 127.0.0.53 (systemd-resolved stub) — it won't
				// resolve container names. Keep 127.0.0.11 (Docker) and others.
				if ip == "127.0.0.53" {
					continue
				}
				servers = append(servers, net.JoinHostPort(ip, "53"))
			}
		}
	}
	return servers
}

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

	upstreamMu.RLock()
	upstreams := make([]string, len(upstreamServers))
	copy(upstreams, upstreamServers)
	upstreamMu.RUnlock()

	for i, upstream := range upstreams {
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
		isLast := i == len(upstreams)-1

		if hasAnswer || isLast {
			if _, err := conn.WriteToUDP(resp[:n], clientAddr); err != nil {
				slog.Error("dns write client", "error", err)
			}
			return
		}
	}

	slog.Error("dns all upstreams failed", "client", clientAddr.String())
}
