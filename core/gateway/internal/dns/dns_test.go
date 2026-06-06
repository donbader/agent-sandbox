package dns

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getFreeUDPAddr returns a free UDP address on localhost.
func getFreeUDPAddr(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := conn.LocalAddr().String()
	_ = conn.Close()
	return addr
}

// startMockDNS starts a mock UDP DNS server that responds using the provided handler.
// The handler receives the raw query bytes and returns raw response bytes (nil = no response).
func startMockDNS(t *testing.T, handler func([]byte) []byte) string {
	t.Helper()
	addr := getFreeUDPAddr(t)
	udpAddr, _ := net.ResolveUDPAddr("udp", addr)
	conn, err := net.ListenUDP("udp", udpAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 4096)
		for {
			n, clientAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			resp := handler(buf[:n])
			if resp != nil {
				conn.WriteToUDP(resp, clientAddr) //nolint:errcheck
			}
		}
	}()
	return addr
}

// buildDNSQuery builds a minimal DNS query for example.com A IN with the given transaction ID.
func buildDNSQuery(id uint16) []byte {
	// Header: ID(2) + Flags(2) + QDCOUNT(2) + ANCOUNT(2) + NSCOUNT(2) + ARCOUNT(2)
	header := []byte{
		byte(id >> 8), byte(id & 0xff), // ID
		0x01, 0x00, // Flags: standard query, recursion desired
		0x00, 0x01, // QDCOUNT: 1
		0x00, 0x00, // ANCOUNT: 0
		0x00, 0x00, // NSCOUNT: 0
		0x00, 0x00, // ARCOUNT: 0
	}
	// Question: example.com A IN
	question := []byte{
		0x07, 'e', 'x', 'a', 'm', 'p', 'l', 'e',
		0x03, 'c', 'o', 'm',
		0x00,       // root label
		0x00, 0x01, // QTYPE: A
		0x00, 0x01, // QCLASS: IN
	}
	return append(header, question...)
}

// startServer starts a DNS Server on a free port and waits briefly for it to be ready.
// Returns the server address.
func startServer(t *testing.T) string {
	t.Helper()
	addr := getFreeUDPAddr(t)
	srv := NewServer(addr)
	go func() {
		_ = srv.ListenAndServe()
	}()
	// Give the server goroutine time to bind.
	time.Sleep(50 * time.Millisecond)
	return addr
}

// sendQuery sends a DNS query to addr and returns the raw response bytes, or an error.
func sendQuery(t *testing.T, addr string, query []byte, deadline time.Duration) ([]byte, error) {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(deadline)))

	_, err = conn.Write(query)
	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// setUpstreams safely replaces upstreamServers and returns a restore func.
func setUpstreams(t *testing.T, addrs []string) {
	t.Helper()
	upstreamMu.Lock()
	orig := upstreamServers
	upstreamServers = addrs
	upstreamMu.Unlock()
	t.Cleanup(func() {
		upstreamMu.Lock()
		upstreamServers = orig
		upstreamMu.Unlock()
	})
}

// TestDNS_ForwardsQuery verifies that the server forwards a query to the upstream
// and returns the upstream's response to the client with matching transaction ID.
func TestDNS_ForwardsQuery(t *testing.T) {
	mockAddr := startMockDNS(t, func(query []byte) []byte {
		if len(query) < 12 {
			return nil
		}
		// Echo back a response: copy query, set QR bit, set ANCOUNT=1.
		resp := make([]byte, len(query))
		copy(resp, query)
		resp[2] |= 0x80 // QR bit = response
		resp[6] = 0x00  // ANCOUNT high byte
		resp[7] = 0x01  // ANCOUNT low byte = 1
		return resp
	})

	setUpstreams(t, []string{mockAddr})
	srvAddr := startServer(t)

	const queryID uint16 = 0xABCD
	query := buildDNSQuery(queryID)

	resp, err := sendQuery(t, srvAddr, query, 2*time.Second)
	require.NoError(t, err, "expected a response from the DNS server")
	require.GreaterOrEqual(t, len(resp), 2, "response too short to contain ID")

	gotID := uint16(resp[0])<<8 | uint16(resp[1])
	assert.Equal(t, queryID, gotID, "response transaction ID should match query ID")
}

// TestDNS_FallbackOnNXDOMAIN verifies that when the first upstream returns no answer
// (ANCOUNT=0), the server tries the second upstream and returns its response.
func TestDNS_FallbackOnNXDOMAIN(t *testing.T) {
	// First mock: returns NXDOMAIN-style response (QR bit set, ANCOUNT=0).
	mock1Addr := startMockDNS(t, func(query []byte) []byte {
		if len(query) < 12 {
			return nil
		}
		resp := make([]byte, 12)
		copy(resp, query[:12])
		resp[2] |= 0x80 // QR bit
		resp[6] = 0x00  // ANCOUNT = 0
		resp[7] = 0x00
		return resp
	})

	// Second mock: returns a real answer (ANCOUNT=1) with a marker byte at position 11.
	const markerByte byte = 0x42
	mock2Addr := startMockDNS(t, func(query []byte) []byte {
		if len(query) < 12 {
			return nil
		}
		resp := make([]byte, 12)
		copy(resp, query[:12])
		resp[2] |= 0x80       // QR bit
		resp[6] = 0x00        // ANCOUNT high byte
		resp[7] = 0x01        // ANCOUNT low byte = 1
		resp[11] = markerByte // marker to identify this came from mock2
		return resp
	})

	setUpstreams(t, []string{mock1Addr, mock2Addr})
	srvAddr := startServer(t)

	query := buildDNSQuery(0x1234)

	resp, err := sendQuery(t, srvAddr, query, 2*time.Second)
	require.NoError(t, err, "expected a response after fallback to second upstream")
	require.GreaterOrEqual(t, len(resp), 12, "response too short")

	assert.True(t, resp[6] > 0 || resp[7] > 0, "response should have ANCOUNT > 0 (from second upstream)")
	assert.Equal(t, markerByte, resp[11], "response should carry marker byte from second upstream")
}

// TestDNS_AllUpstreamsFail verifies that the server does not crash when all upstreams
// are unreachable. The client should receive no response within the deadline.
func TestDNS_AllUpstreamsFail(t *testing.T) {
	// TEST-NET (RFC 5737) addresses — routable but guaranteed never to respond.
	setUpstreams(t, []string{"192.0.2.1:53", "192.0.2.2:53"})

	srvAddr := startServer(t)
	query := buildDNSQuery(0xDEAD)

	_, err := sendQuery(t, srvAddr, query, 300*time.Millisecond)
	assert.Error(t, err, "expected a timeout error — no response from server when all upstreams fail")
	// Verify it's a timeout, not some other error.
	netErr, ok := err.(net.Error)
	assert.True(t, ok && netErr.Timeout(), "error should be a network timeout, got: %v", err)
}

func TestParseResolvConf(t *testing.T) {
	t.Run("extracts nameservers", func(t *testing.T) {
		tmp := t.TempDir()
		path := tmp + "/resolv.conf"
		content := "# comment\nnameserver 10.0.0.1\nnameserver 172.17.0.1\nsearch example.com\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		servers := parseResolvConf(path)
		assert.Equal(t, []string{"10.0.0.1:53", "172.17.0.1:53"}, servers)
	})

	t.Run("skips systemd-resolved stub", func(t *testing.T) {
		tmp := t.TempDir()
		path := tmp + "/resolv.conf"
		content := "nameserver 127.0.0.53\nnameserver 8.8.4.4\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		servers := parseResolvConf(path)
		assert.Equal(t, []string{"8.8.4.4:53"}, servers)
	})

	t.Run("missing file returns nil", func(t *testing.T) {
		servers := parseResolvConf("/nonexistent/resolv.conf")
		assert.Nil(t, servers)
	})

	t.Run("keeps docker DNS 127.0.0.11", func(t *testing.T) {
		tmp := t.TempDir()
		path := tmp + "/resolv.conf"
		content := "nameserver 127.0.0.11\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		servers := parseResolvConf(path)
		assert.Equal(t, []string{"127.0.0.11:53"}, servers)
	})

	t.Run("skips invalid IP addresses", func(t *testing.T) {
		tmp := t.TempDir()
		path := tmp + "/resolv.conf"
		content := "nameserver not-an-ip\nnameserver 10.0.0.1\nnameserver abc.def\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		servers := parseResolvConf(path)
		assert.Equal(t, []string{"10.0.0.1:53"}, servers)
	})

	t.Run("empty file returns nil", func(t *testing.T) {
		tmp := t.TempDir()
		path := tmp + "/resolv.conf"
		require.NoError(t, os.WriteFile(path, []byte("# just a comment\nsearch example.com\n"), 0644))

		servers := parseResolvConf(path)
		assert.Nil(t, servers)
	})
}

func TestInitUpstreamServers_FallbackDedup(t *testing.T) {
	// If resolv.conf already contains 8.8.8.8, it shouldn't appear twice.
	tmp := t.TempDir()
	path := tmp + "/resolv.conf"
	content := "nameserver 8.8.8.8\nnameserver 10.0.0.1\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	servers := parseResolvConf(path)
	// Simulate initUpstreamServers logic
	for _, fb := range PublicDNSFallbacks {
		if !contains(servers, fb) {
			servers = append(servers, fb)
		}
	}

	// 8.8.8.8 should appear exactly once
	count := 0
	for _, s := range servers {
		if s == "8.8.8.8:53" {
			count++
		}
	}
	assert.Equal(t, 1, count, "8.8.8.8:53 should not be duplicated")
	// 1.1.1.1 added as fallback
	assert.Contains(t, servers, "1.1.1.1:53")
}

func TestInitUpstreamServers_EmptyResolvConf(t *testing.T) {
	// When resolv.conf has no nameservers, fallbacks must still be present.
	tmp := t.TempDir()
	path := tmp + "/resolv.conf"
	require.NoError(t, os.WriteFile(path, []byte("# empty\n"), 0644))

	servers := parseResolvConf(path)
	for _, fb := range PublicDNSFallbacks {
		if !contains(servers, fb) {
			servers = append(servers, fb)
		}
	}

	assert.Equal(t, PublicDNSFallbacks, servers)
}
