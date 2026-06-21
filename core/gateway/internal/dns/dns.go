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

// PublicDNSFallbacks are well-known public resolvers used when resolv.conf
// yields no usable nameservers. Two providers for redundancy.
var PublicDNSFallbacks = []string{"8.8.8.8:53", "1.1.1.1:53"}

// initUpstreamServers reads nameservers from /etc/resolv.conf and appends
// public DNS fallbacks. This makes the gateway work with any container runtime
// (Docker, Podman, containerd) without hardcoding runtime-specific DNS addresses.
func initUpstreamServers() []string {
	servers := parseResolvConf("/etc/resolv.conf")
	if len(servers) == 0 {
		slog.Warn("dns: no usable nameservers in /etc/resolv.conf, using public DNS only")
	}
	// Always add public DNS as final fallback (deduped against resolv.conf entries)
	for _, fb := range PublicDNSFallbacks {
		if !contains(servers, fb) {
			servers = append(servers, fb)
		}
	}
	return servers
}

// contains checks if a string exists in a slice.
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// parseResolvConf extracts nameserver entries from a resolv.conf file.
// Entries with invalid IP addresses are skipped with a warning.
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
				// Validate IP format
				if net.ParseIP(ip) == nil {
					slog.Warn("dns: skipping invalid nameserver IP in resolv.conf", "ip", ip)
					continue
				}
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

// Server is a UDP DNS forwarder that intercepts configured domains.
type Server struct {
	listen       string
	interceptIPs map[string]net.IP // domain → IP to respond with
	interceptAll net.IP            // if set, respond with this IP for all non-Docker queries
}

// NewServer creates a DNS server listening on the given address.
func NewServer(listen string) *Server {
	return &Server{listen: listen, interceptIPs: make(map[string]net.IP)}
}

// InterceptDomains configures the server to respond to A queries for the
// given domains with the specified IP address (instead of forwarding upstream).
// This allows the gateway to attract traffic for MITM domains directly,
// avoiding reliance on iptables PREROUTING in Docker internal networks.
func (s *Server) InterceptDomains(domains []string, ip string) {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		slog.Warn("dns: invalid intercept IP, skipping domain interception", "ip", ip)
		return
	}
	for _, d := range domains {
		// Normalize: ensure trailing dot for comparison with wire format
		s.interceptIPs[strings.TrimSuffix(strings.ToLower(d), ".")] = parsed
	}
	slog.Info("dns: intercepting domains", "count", len(domains), "ip", ip)
}

// InterceptAll configures the server to respond with the gateway IP for ALL
// A queries that Docker DNS cannot resolve (i.e. external domains).
// This is used when a wildcard egress rule ("*") is configured.
func (s *Server) InterceptAll(ip string) {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		slog.Warn("dns: invalid intercept-all IP", "ip", ip)
		return
	}
	s.interceptAll = parsed
	slog.Info("dns: intercept-all mode enabled", "ip", ip)
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

	// Block AAAA (IPv6) queries: the gateway only has IPv4 DNAT rules,
	// so IPv6 addresses would bypass routing and fail with "network unreachable".
	if isAAAAQuery(query) {
		if resp := synthesizeEmptyResponse(query); resp != nil {
			if _, err := conn.WriteToUDP(resp, clientAddr); err != nil {
				slog.Error("dns write client (AAAA block)", "error", err)
			}
			return
		}
	}

	// Intercept A queries for configured domains (respond with gateway IP).
	if len(s.interceptIPs) > 0 && isAQuery(query) {
		if name := extractQName(query); name != "" {
			if ip, ok := s.interceptIPs[name]; ok {
				if resp := synthesizeAResponse(query, ip); resp != nil {
					slog.Debug("dns intercept", "domain", name, "ip", ip)
					if _, err := conn.WriteToUDP(resp, clientAddr); err != nil {
						slog.Error("dns write client (intercept)", "error", err)
					}
					return
				}
			}
		}
	}

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

		hasAnswer := n > 7 && (resp[6] > 0 || resp[7] > 0)
		isLast := i == len(upstreams)-1

		if hasAnswer {
			// In interceptAll mode: only pass through private IPs (container names).
			// Public IPs mean Docker DNS forwarded externally — override with gateway IP.
			if s.interceptAll != nil && isAQuery(query) {
				if ip := extractFirstARecord(resp[:n]); ip != nil && !isPrivateIP(ip) {
					if synth := synthesizeAResponse(query, s.interceptAll); synth != nil {
						name := extractQName(query)
						slog.Debug("dns intercept-all", "domain", name, "real_ip", ip, "gateway_ip", s.interceptAll)
						if _, err := conn.WriteToUDP(synth, clientAddr); err != nil {
							slog.Error("dns write client (intercept-all)", "error", err)
						}
						return
					}
				}
			}
			if _, err := conn.WriteToUDP(resp[:n], clientAddr); err != nil {
				slog.Error("dns write client", "error", err)
			}
			return
		}

		// No answer — in interceptAll mode, respond with gateway IP
		if s.interceptAll != nil && isAQuery(query) {
			if synth := synthesizeAResponse(query, s.interceptAll); synth != nil {
				name := extractQName(query)
				slog.Debug("dns intercept-all (nxdomain)", "domain", name, "ip", s.interceptAll)
				if _, err := conn.WriteToUDP(synth, clientAddr); err != nil {
					slog.Error("dns write client (intercept-all)", "error", err)
				}
				return
			}
		}

		if isLast {
			if _, err := conn.WriteToUDP(resp[:n], clientAddr); err != nil {
				slog.Error("dns write client", "error", err)
			}
			return
		}
	}

	slog.Error("dns all upstreams failed", "client", clientAddr.String())
}

// isAAAAQuery checks if a DNS query is asking for AAAA records (type 28).
// DNS wire format: 12-byte header, then question section (QNAME + QTYPE + QCLASS).
func isAAAAQuery(query []byte) bool {
	if len(query) < 14 { // minimum: 12 header + 1 label byte + 1 null
		return false
	}
	// Skip header (12 bytes), walk QNAME labels
	i := 12
	for i < len(query) {
		labelLen := int(query[i])
		if labelLen == 0 {
			i++ // skip null terminator
			break
		}
		i += 1 + labelLen
	}
	// QTYPE is next 2 bytes
	if i+2 > len(query) {
		return false
	}
	qtype := uint16(query[i])<<8 | uint16(query[i+1])
	return qtype == 28 // AAAA
}

// isAQuery checks if a DNS query is asking for A records (type 1).
func isAQuery(query []byte) bool {
	if len(query) < 14 {
		return false
	}
	i := 12
	for i < len(query) {
		labelLen := int(query[i])
		if labelLen == 0 {
			i++
			break
		}
		i += 1 + labelLen
	}
	if i+2 > len(query) {
		return false
	}
	qtype := uint16(query[i])<<8 | uint16(query[i+1])
	return qtype == 1 // A
}

// extractQName extracts the queried domain name from a DNS query in wire format.
// Returns lowercase name without trailing dot.
func extractQName(query []byte) string {
	if len(query) < 13 {
		return ""
	}
	var labels []string
	i := 12
	for i < len(query) {
		labelLen := int(query[i])
		if labelLen == 0 {
			break
		}
		i++
		if i+labelLen > len(query) {
			return ""
		}
		labels = append(labels, string(query[i:i+labelLen]))
		i += labelLen
	}
	return strings.ToLower(strings.Join(labels, "."))
}

// synthesizeAResponse creates a DNS A response with the given IPv4 address.
func synthesizeAResponse(query []byte, ip net.IP) []byte {
	if len(query) < 12 {
		return nil
	}
	// Find end of question section (QNAME + QTYPE + QCLASS)
	i := 12
	for i < len(query) {
		labelLen := int(query[i])
		if labelLen == 0 {
			i++ // null terminator
			break
		}
		i += 1 + labelLen
	}
	i += 4 // QTYPE (2) + QCLASS (2)
	if i > len(query) {
		return nil
	}

	// Build response: header + question + answer
	resp := make([]byte, i+16) // question section + 16 bytes for A answer
	copy(resp, query[:i])      // copy header + question

	// Header flags: QR=1, AA=1, RD=1, RA=1
	resp[2] = 0x85 // QR + AA + RD
	resp[3] = 0x80 // RA
	// ANCOUNT = 1
	resp[6] = 0
	resp[7] = 1
	// NSCOUNT = 0, ARCOUNT = 0
	resp[8] = 0
	resp[9] = 0
	resp[10] = 0
	resp[11] = 0

	// Answer section: pointer to QNAME + type A + class IN + TTL + RDLENGTH + IP
	ans := resp[i:]
	ans[0] = 0xC0 // pointer to offset 12 (QNAME)
	ans[1] = 0x0C
	ans[2] = 0x00 // TYPE = A (1)
	ans[3] = 0x01
	ans[4] = 0x00 // CLASS = IN (1)
	ans[5] = 0x01
	ans[6] = 0x00 // TTL = 60 seconds
	ans[7] = 0x00
	ans[8] = 0x00
	ans[9] = 0x3C
	ans[10] = 0x00 // RDLENGTH = 4
	ans[11] = 0x04
	copy(ans[12:16], ip.To4())

	return resp
}

// synthesizeEmptyResponse creates a DNS response with zero answers for the given query.
// This tells the client "no records of this type exist" without hitting upstream.
func synthesizeEmptyResponse(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	resp := make([]byte, len(query))
	copy(resp, query)
	// Set QR bit (response), keep RD, set RA
	resp[2] = resp[2] | 0x80 // QR = 1
	resp[3] = resp[3] | 0x80 // RA = 1
	// Zero out answer/authority/additional counts
	resp[6] = 0
	resp[7] = 0
	resp[8] = 0
	resp[9] = 0
	resp[10] = 0
	resp[11] = 0
	return resp
}

// extractFirstARecord extracts the first A record IP from a DNS response.
// Returns nil if no A record is found.
func extractFirstARecord(resp []byte) net.IP {
	if len(resp) < 12 {
		return nil
	}
	// Skip header
	anCount := int(resp[6])<<8 | int(resp[7])
	if anCount == 0 {
		return nil
	}
	// Skip question section
	i := 12
	for i < len(resp) {
		labelLen := int(resp[i])
		if labelLen == 0 {
			i++ // null terminator
			break
		}
		i += 1 + labelLen
	}
	i += 4 // QTYPE + QCLASS

	// Walk answer records
	for an := 0; an < anCount && i < len(resp); an++ {
		// Name: pointer (2 bytes) or labels
		if i+2 > len(resp) {
			return nil
		}
		if resp[i]&0xC0 == 0xC0 {
			i += 2 // pointer
		} else {
			for i < len(resp) {
				labelLen := int(resp[i])
				if labelLen == 0 {
					i++
					break
				}
				i += 1 + labelLen
			}
		}
		// TYPE(2) + CLASS(2) + TTL(4) + RDLENGTH(2) = 10
		if i+10 > len(resp) {
			return nil
		}
		rtype := uint16(resp[i])<<8 | uint16(resp[i+1])
		rdlen := int(resp[i+8])<<8 | int(resp[i+9])
		i += 10
		if i+rdlen > len(resp) {
			return nil
		}
		if rtype == 1 && rdlen == 4 { // A record
			return net.IP(resp[i : i+4])
		}
		i += rdlen
	}
	return nil
}

// isPrivateIP returns true if the IP is in a private/reserved range
// (container names resolve to these; external domains don't).
func isPrivateIP(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// 10.0.0.0/8
	if ip4[0] == 10 {
		return true
	}
	// 172.16.0.0/12
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	// 192.168.0.0/16
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	// 127.0.0.0/8
	if ip4[0] == 127 {
		return true
	}
	return false
}
