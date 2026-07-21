package proxy

import (
	"fmt"
	"io"
	"net"
	"time"
)

// VPNDialer dials outbound TCP connections through a configured VPN proxy.
type VPNDialer struct {
	profile VPNProfile
}

// Dial connects to addr (host:port) through the VPN proxy.
func (d *VPNDialer) Dial(network, addr string) (net.Conn, error) {
	switch d.profile.Type {
	case "socks5":
		return socks5Dial(d.profile.Address, addr)
	default:
		return nil, fmt.Errorf("vpn: unsupported type %q", d.profile.Type)
	}
}

// BuildVPNDialers constructs a VPNDialer for each named profile.
// Callers can look up a profile by name and call Dial on the result.
func BuildVPNDialers(profiles map[string]VPNProfile) map[string]*VPNDialer {
	out := make(map[string]*VPNDialer, len(profiles))
	for name, p := range profiles {
		out[name] = &VPNDialer{profile: p}
	}
	return out
}

// socks5Dial connects to targetAddr via a no-auth SOCKS5 proxy at proxyAddr.
//
// Protocol (RFC 1928):
//
//	Client → Proxy: VER(1) NMETHODS(1) METHODS(n)   — greeting
//	Proxy  → Client: VER(1) METHOD(1)               — method selection (0x00 = no auth)
//	Client → Proxy: VER(1) CMD(1) RSV(1) ATYP(1) DST.ADDR DST.PORT — request
//	Proxy  → Client: VER(1) REP(1) RSV(1) ATYP(1) BND.ADDR BND.PORT — reply
func socks5Dial(proxyAddr, targetAddr string) (net.Conn, error) {
	// Validate target address before opening any connections.
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, fmt.Errorf("socks5: parse target address %q: %w", targetAddr, err)
	}
	if len(host) > 255 {
		return nil, fmt.Errorf("socks5: hostname too long (%d bytes)", len(host))
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return nil, fmt.Errorf("socks5: parse port %q: %w", portStr, err)
	}

	conn, err := net.DialTimeout("tcp", proxyAddr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("socks5: connect to proxy %s: %w", proxyAddr, err)
	}

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: set deadline: %w", err)
	}

	// --- Greeting ---
	// Request no-auth (method 0x00).
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: send greeting: %w", err)
	}

	// Read server method selection.
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: read method response: %w", err)
	}
	if resp[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("socks5: unexpected version %d in method response", resp[0])
	}
	if resp[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5: proxy requires authentication (method 0x%02x), only no-auth is supported", resp[1])
	}

	// --- Connect request ---
	// Build CONNECT request. Use ATYP 0x03 (domain name) for all targets so we
	// avoid resolving the hostname locally — the proxy resolves it instead, which
	// is important for VPN split-tunnel scenarios.

	req := make([]byte, 0, 7+len(host))
	req = append(req,
		0x05,       // VER
		0x01,       // CMD = CONNECT
		0x00,       // RSV
		0x03,       // ATYP = domain name
		byte(len(host)), // length of domain name
	)
	req = append(req, []byte(host)...)
	req = append(req, byte(port>>8), byte(port)) //nolint:gosec // port fits in uint16

	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: send connect request: %w", err)
	}

	// --- Read reply ---
	// Minimum reply header: VER(1) REP(1) RSV(1) ATYP(1) = 4 bytes.
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: read reply header: %w", err)
	}
	if header[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("socks5: unexpected version %d in reply", header[0])
	}
	if header[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5: connect failed: %s", socks5ReplyText(header[1]))
	}

	// Consume the bound address from the reply so the connection is ready to use.
	if err := socks5DrainBoundAddr(conn, header[3]); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: drain bound address: %w", err)
	}

	// Clear deadline — caller controls timeouts from here.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("socks5: clear deadline: %w", err)
	}

	return conn, nil
}

// socks5DrainBoundAddr reads and discards the BND.ADDR + BND.PORT fields from
// the SOCKS5 reply based on the address type (ATYP).
func socks5DrainBoundAddr(r io.Reader, atyp byte) error {
	switch atyp {
	case 0x01: // IPv4: 4 bytes + 2 port
		_, err := io.ReadFull(r, make([]byte, 6))
		return err
	case 0x03: // Domain name: 1-byte length + name + 2 port
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return err
		}
		_, err := io.ReadFull(r, make([]byte, int(lenBuf[0])+2))
		return err
	case 0x04: // IPv6: 16 bytes + 2 port
		_, err := io.ReadFull(r, make([]byte, 18))
		return err
	default:
		return fmt.Errorf("unknown address type 0x%02x", atyp)
	}
}

// socks5ReplyText converts a SOCKS5 reply code to a human-readable message.
func socks5ReplyText(code byte) string {
	switch code {
	case 0x01:
		return "general SOCKS server failure"
	case 0x02:
		return "connection not allowed by ruleset"
	case 0x03:
		return "network unreachable"
	case 0x04:
		return "host unreachable"
	case 0x05:
		return "connection refused"
	case 0x06:
		return "TTL expired"
	case 0x07:
		return "command not supported"
	case 0x08:
		return "address type not supported"
	default:
		return fmt.Sprintf("unknown error code 0x%02x", code)
	}
}


