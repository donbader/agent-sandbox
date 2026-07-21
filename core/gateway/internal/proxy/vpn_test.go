package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Config validation tests ---

func TestLoadConfig_VPNProfiles_Valid(t *testing.T) {
	cfg := writeConfigFile(t, `
listen: ":8443"
vpn_profiles:
  corp-vpn:
    type: socks5
    address: "vpn-proxy:1080"
egress_rules:
  - hosts: ["internal.corp.com"]
    vpn: corp-vpn
  - hosts: ["*"]
`)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.VPNProfiles, 1)
	assert.Equal(t, "socks5", cfg.VPNProfiles["corp-vpn"].Type)
	assert.Equal(t, "vpn-proxy:1080", cfg.VPNProfiles["corp-vpn"].Address)
	assert.Equal(t, "corp-vpn", cfg.EgressRules[0].VPN)
}

func TestLoadConfig_VPNProfiles_MultipleProfiles(t *testing.T) {
	cfg := writeConfigFile(t, `
listen: ":8443"
vpn_profiles:
  vpn-a:
    type: socks5
    address: "vpn-a:1080"
  vpn-b:
    type: socks5
    address: "vpn-b:1081"
egress_rules:
  - hosts: ["a.internal"]
    vpn: vpn-a
  - hosts: ["b.internal"]
    vpn: vpn-b
  - hosts: ["*"]
`)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.VPNProfiles, 2)
	assert.Equal(t, "vpn-a", cfg.EgressRules[0].VPN)
	assert.Equal(t, "vpn-b", cfg.EgressRules[1].VPN)
	assert.Empty(t, cfg.EgressRules[2].VPN)
}

func TestLoadConfig_VPNProfiles_UndefinedReference(t *testing.T) {
	_, err := loadConfigFromString(t, `
listen: ":8443"
egress_rules:
  - hosts: ["internal.corp.com"]
    vpn: nonexistent-vpn
  - hosts: ["*"]
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-vpn")
	assert.Contains(t, err.Error(), "not defined in vpn_profiles")
}

func TestLoadConfig_VPNProfiles_MissingType(t *testing.T) {
	_, err := loadConfigFromString(t, `
listen: ":8443"
vpn_profiles:
  bad-vpn:
    address: "vpn-proxy:1080"
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad-vpn")
	assert.Contains(t, err.Error(), "type is required")
}

func TestLoadConfig_VPNProfiles_UnsupportedType(t *testing.T) {
	_, err := loadConfigFromString(t, `
listen: ":8443"
vpn_profiles:
  bad-vpn:
    type: http-connect
    address: "vpn-proxy:3128"
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad-vpn")
	assert.Contains(t, err.Error(), "unsupported type")
	assert.Contains(t, err.Error(), "http-connect")
}

func TestLoadConfig_VPNProfiles_MissingAddress(t *testing.T) {
	_, err := loadConfigFromString(t, `
listen: ":8443"
vpn_profiles:
  bad-vpn:
    type: socks5
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad-vpn")
	assert.Contains(t, err.Error(), "address is required")
}

func TestLoadConfig_VPNProfiles_CombinedWithDeny(t *testing.T) {
	_, err := loadConfigFromString(t, `
listen: ":8443"
vpn_profiles:
  corp-vpn:
    type: socks5
    address: "vpn-proxy:1080"
egress_rules:
  - hosts: ["blocked.com"]
    deny: true
    vpn: corp-vpn
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vpn cannot be combined with deny")
}

func TestLoadConfig_VPNProfiles_NoProfiles_NoError(t *testing.T) {
	cfg := writeConfigFile(t, `
listen: ":8443"
egress_rules:
  - hosts: ["*"]
`)
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.VPNProfiles)
}

// --- BuildVPNDialers tests ---

func TestBuildVPNDialers_EmptyProfiles(t *testing.T) {
	dialers := BuildVPNDialers(nil)
	assert.Empty(t, dialers)
}

func TestBuildVPNDialers_PopulatesMap(t *testing.T) {
	profiles := map[string]VPNProfile{
		"vpn-a": {Type: "socks5", Address: "proxy-a:1080"},
		"vpn-b": {Type: "socks5", Address: "proxy-b:1081"},
	}
	dialers := BuildVPNDialers(profiles)
	assert.Len(t, dialers, 2)
	assert.NotNil(t, dialers["vpn-a"])
	assert.NotNil(t, dialers["vpn-b"])
}

// --- SOCKS5 dialer tests ---

// TestSOCKS5Dial_Success verifies that socks5Dial performs a correct SOCKS5 handshake
// and returns a usable connection when the proxy replies with success.
func TestSOCKS5Dial_Success(t *testing.T) {
	// Start a mock SOCKS5 server that accepts a connection to "example.com:443"
	targetSent := make(chan string, 1)
	ln := startMockSOCKS5Server(t, socks5Reply{code: 0x00}, targetSent)
	defer ln.Close()

	conn, err := socks5Dial(ln.Addr().String(), "example.com:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()

	// Verify the proxy received the correct destination.
	assert.Equal(t, "example.com:443", <-targetSent)
}

// TestSOCKS5Dial_AuthRequired verifies that socks5Dial fails when the proxy
// requires authentication (returns method 0xFF = no acceptable method).
func TestSOCKS5Dial_AuthRequired(t *testing.T) {
	ln := startMockSOCKS5Server(t, socks5Reply{rejectAuth: true}, nil)
	defer ln.Close()

	_, err := socks5Dial(ln.Addr().String(), "example.com:443")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "auth")
}

// TestSOCKS5Dial_ConnectRefused verifies that socks5Dial returns a descriptive error
// when the proxy reports connection refused (reply code 0x05).
func TestSOCKS5Dial_ConnectRefused(t *testing.T) {
	ln := startMockSOCKS5Server(t, socks5Reply{code: 0x05}, nil)
	defer ln.Close()

	_, err := socks5Dial(ln.Addr().String(), "example.com:443")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "refused")
}

// TestSOCKS5Dial_HostnameTooLong verifies validation before the network call.
func TestSOCKS5Dial_HostnameTooLong(t *testing.T) {
	longHost := strings.Repeat("a", 256) + ".example.com"
	_, err := socks5Dial("127.0.0.1:9999", longHost+":443")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "too long")
}

// TestSOCKS5Dial_ProxyUnreachable verifies a clear error when the proxy is down.
func TestSOCKS5Dial_ProxyUnreachable(t *testing.T) {
	_, err := socks5Dial("127.0.0.1:1", "example.com:443")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "socks5")
}

// TestVPNDialer_Dial_Socks5 verifies that VPNDialer.Dial routes through SOCKS5.
func TestVPNDialer_Dial_Socks5(t *testing.T) {
	ln := startMockSOCKS5Server(t, socks5Reply{code: 0x00}, nil)
	defer ln.Close()

	d := &VPNDialer{profile: VPNProfile{Type: "socks5", Address: ln.Addr().String()}}
	conn, err := d.Dial("tcp", "example.com:443")
	require.NoError(t, err)
	conn.Close()
}

// TestVPNDialer_Dial_UnsupportedType verifies VPNDialer returns an error for unknown types.
func TestVPNDialer_Dial_UnsupportedType(t *testing.T) {
	d := &VPNDialer{profile: VPNProfile{Type: "unknown", Address: "proxy:1080"}}
	_, err := d.Dial("tcp", "example.com:443")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type")
}

// TestSOCKS5_ReplyText verifies human-readable error messages for known reply codes.
func TestSOCKS5_ReplyText(t *testing.T) {
	cases := []struct {
		code byte
		want string
	}{
		{0x01, "general"},
		{0x02, "ruleset"},
		{0x03, "network unreachable"},
		{0x04, "host unreachable"},
		{0x05, "connection refused"},
		{0x06, "TTL"},
		{0x07, "command not supported"},
		{0x08, "address type"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("0x%02x", tc.code), func(t *testing.T) {
			text := socks5ReplyText(tc.code)
			assert.Contains(t, strings.ToLower(text), strings.ToLower(tc.want))
		})
	}
}

// --- helpers ---

// TestSOCKS5Dial_DomainBoundAddr verifies that socks5Dial correctly drains a
// domain-name BND.ADDR (ATYP=0x03) in the SOCKS5 reply.
func TestSOCKS5Dial_DomainBoundAddr(t *testing.T) {
	ln := startMockSOCKS5Server(t, socks5Reply{code: 0x00, bindAtyp: 0x03}, nil)
	defer ln.Close()

	conn, err := socks5Dial(ln.Addr().String(), "example.com:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()
}

// TestSOCKS5Dial_IPv6BoundAddr verifies that socks5Dial correctly drains an
// IPv6 BND.ADDR (ATYP=0x04) in the SOCKS5 reply.
func TestSOCKS5Dial_IPv6BoundAddr(t *testing.T) {
	ln := startMockSOCKS5Server(t, socks5Reply{code: 0x00, bindAtyp: 0x04}, nil)
	defer ln.Close()

	conn, err := socks5Dial(ln.Addr().String(), "example.com:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()
}

// socks5Reply configures what the mock SOCKS5 server sends back.
type socks5Reply struct {
	rejectAuth bool   // send 0xFF (no acceptable method) instead of 0x00
	code       byte   // SOCKS5 reply code (0x00 = success)
	bindAtyp   byte   // ATYP for BND.ADDR in success reply (0 = default 0x01 IPv4)
}

// startMockSOCKS5Server starts a TCP listener that speaks SOCKS5 and returns
// a channel that receives the requested "host:port" string after each successful
// connect request (nil channel = don't capture). The server handles one connection.
func startMockSOCKS5Server(t *testing.T, reply socks5Reply, targetOut chan<- string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		defer conn.Close()

		// --- Greeting ---
		greeting := make([]byte, 3)
		if _, err := io.ReadFull(conn, greeting); err != nil {
			return
		}
		if reply.rejectAuth {
			conn.Write([]byte{0x05, 0xFF}) //nolint:errcheck
			return
		}
		conn.Write([]byte{0x05, 0x00}) //nolint:errcheck

		// --- Connect request ---
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		// Read destination based on ATYP.
		var target string
		switch header[3] {
		case 0x01: // IPv4
			buf := make([]byte, 6)
			io.ReadFull(conn, buf) //nolint:errcheck
			ip := net.IP(buf[:4])
			port := binary.BigEndian.Uint16(buf[4:])
			target = fmt.Sprintf("%s:%d", ip, port)
		case 0x03: // Domain
			lenBuf := make([]byte, 1)
			io.ReadFull(conn, lenBuf) //nolint:errcheck
			nameBuf := make([]byte, int(lenBuf[0])+2)
			io.ReadFull(conn, nameBuf) //nolint:errcheck
			port := binary.BigEndian.Uint16(nameBuf[len(nameBuf)-2:])
			target = fmt.Sprintf("%s:%d", nameBuf[:len(nameBuf)-2], port)
		case 0x04: // IPv6
			buf := make([]byte, 18)
			io.ReadFull(conn, buf) //nolint:errcheck
		}

		if targetOut != nil {
			targetOut <- target
		}

		// --- Reply ---
		if reply.code != 0x00 {
			conn.Write([]byte{0x05, reply.code, 0x00, 0x01, 0, 0, 0, 0, 0, 0}) //nolint:errcheck
			return
		}
		// Success — BND.ADDR format depends on bindAtyp (default: IPv4).
		atyp := reply.bindAtyp
		if atyp == 0 {
			atyp = 0x01
		}
		switch atyp {
		case 0x01: // IPv4: 4 bytes + 2 port
			conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x1F, 0x90}) //nolint:errcheck
		case 0x03: // Domain: length(1) + "lo"(2) + port(2)
			conn.Write([]byte{0x05, 0x00, 0x00, 0x03, 0x02, 'l', 'o', 0x1F, 0x90}) //nolint:errcheck
		case 0x04: // IPv6: 16 bytes + port(2)
			reply6 := []byte{0x05, 0x00, 0x00, 0x04}
			reply6 = append(reply6, make([]byte, 16)...) // ::0
			reply6 = append(reply6, 0x1F, 0x90)         // port 8080
			conn.Write(reply6)                          //nolint:errcheck
		}
		// Keep connection open so caller can use it.
		io.Copy(io.Discard, conn) //nolint:errcheck
	}()

	return ln
}

// writeConfigFile writes YAML to a temp file, calls LoadConfig, and returns cfg.
// Fails the test on any error.
func writeConfigFile(t *testing.T, yaml string) *Config {
	t.Helper()
	cfg, err := loadConfigFromString(t, yaml)
	require.NoError(t, err)
	return cfg
}

// loadConfigFromString writes yaml to a temp file and calls LoadConfig.
func loadConfigFromString(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "gateway-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(yaml)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return LoadConfig(f.Name())
}
