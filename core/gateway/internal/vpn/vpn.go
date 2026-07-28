// Package vpn manages per-profile OpenVPN tunnels for the gateway.
// Each profile starts an openvpn daemon, waits for the tun interface
// to come up, then creates a net.Dialer bound to that interface via
// SO_BINDTODEVICE so specific upstream connections route through the tunnel.
package vpn

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// startupTimeout is how long we wait for a tun interface to appear after
	// launching openvpn before giving up.
	startupTimeout = 60 * time.Second

	// pollInterval is how often we poll for the tun interface.
	pollInterval = 500 * time.Millisecond
)

// ProfileConfig is the VPN profile configuration passed from the gateway config.
type ProfileConfig struct {
	Type      string // "openvpn"
	ConfigB64 string // base64-encoded .ovpn file content
	Username   string // openvpn auth username
	Password   string // openvpn static password (concatenated before TOTP code)
	TOTPSecret string // base32-encoded TOTP secret (RFC 6238); empty = no TOTP
}

// StartTunnel decodes the profile config, writes it to a temp file, launches
// an openvpn daemon pinned to the given tun device, and waits for the
// interface to come up. Idempotent: if the interface is already up, returns nil.
func StartTunnel(name string, profile ProfileConfig, tunIface string) error {
	return startTunnel(name, profile, tunIface)
}

// NewBoundDialer returns a Dialer whose TCP sockets are bound to iface via
// SO_BINDTODEVICE, forcing connections through that network interface.
func NewBoundDialer(iface string, timeout time.Duration) *net.Dialer {
	return newBoundDialer(iface, timeout)
}

// startTunnel is the internal implementation of StartTunnel.
func startTunnel(name string, profile ProfileConfig, tunIface string) error {
	if profile.Type != "openvpn" {
		return fmt.Errorf("unsupported VPN type %q (only 'openvpn' is supported)", profile.Type)
	}

	// Idempotency: if the interface is already up, reuse it.
	if ifaceIsUp(tunIface) {
		slog.Info("vpn tunnel already running, reusing interface", "profile", name, "iface", tunIface)
		return nil
	}

	// Decode the base64 .ovpn config.
	ovpnBytes, err := base64.StdEncoding.DecodeString(profile.ConfigB64)
	if err != nil {
		// Try URL-safe encoding as a fallback.
		ovpnBytes, err = base64.URLEncoding.DecodeString(profile.ConfigB64)
		if err != nil {
			return fmt.Errorf("decode config_b64: %w", err)
		}
	}

	// Write config to a temp file.
	f, err := os.CreateTemp("", fmt.Sprintf("vpn-%s-*.ovpn", name))
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	cfgPath := f.Name()
	if _, err := f.Write(ovpnBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(cfgPath)
		return fmt.Errorf("write temp config: %w", err)
	}
	_ = f.Close()

	// Build openvpn args.
	logPath := fmt.Sprintf("/tmp/vpn-%s.log", name)
	args := []string{
		"--config", cfgPath,
		"--dev", tunIface,
		"--daemon",
		"--log", logPath,
		"--script-security", "2",
	}

	// If credentials are configured, use the OpenVPN management interface for
	// authentication instead of writing credentials to disk. This lets openvpn
	// query us for a fresh TOTP code on every connect and reconnect.
	if profile.Username != "" || profile.Password != "" || profile.TOTPSecret != "" {
		port := tunManagementPort(tunIface)
		args = append(args,
			"--management", "127.0.0.1", fmt.Sprintf("%d", port),
			"--management-query-passwords",
		)
		go serveManagement(fmt.Sprintf("127.0.0.1:%d", port), profile)
	}

	cmd := exec.Command("openvpn", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(cfgPath)
		return fmt.Errorf("openvpn start failed: %w\n%s", err, out)
	}

	// Poll until the tun interface appears and is UP. The config file is deleted
	// once the interface comes up (openvpn has read it by then).
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if ifaceIsUp(tunIface) {
			_ = os.Remove(cfgPath)
			slog.Info("vpn tunnel started", "profile", name, "iface", tunIface)
			return nil
		}
		time.Sleep(pollInterval)
	}

	_ = os.Remove(cfgPath)
	return fmt.Errorf("tun interface %q did not come up within %s — check %s", tunIface, startupTimeout, logPath)
}

// ifaceIsUp returns true if the named network interface exists and has the UP flag.
func ifaceIsUp(name string) bool {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return false
	}
	return iface.Flags&net.FlagUp != 0
}

// tunManagementPort returns the OpenVPN management socket port for a tun interface.
// tun0 → 11940, tun1 → 11941, and so on.
func tunManagementPort(tunIface string) int {
	var idx int
	_, _ = fmt.Sscanf(tunIface, "tun%d", &idx)
	return 11940 + idx
}

// serveManagement connects to the OpenVPN management interface and responds to
// credential queries. It runs in a goroutine for the lifetime of the gateway,
// reconnecting automatically when the connection drops (e.g. on openvpn reconnect).
// On each auth query a fresh TOTP code is generated so every reconnect uses a
// valid, unexpired credential.
func serveManagement(addr string, profile ProfileConfig) {
	const retryDelay = 500 * time.Millisecond
	for {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			time.Sleep(retryDelay)
			continue
		}
		if err := handleManagementConn(conn, profile); err != nil {
			slog.Debug("vpn management connection closed", "addr", addr, "error", err)
		}
		_ = conn.Close()
		time.Sleep(retryDelay)
	}
}

// handleManagementConn reads from an OpenVPN management socket and responds to
// password queries. Returns when the connection is closed or an error occurs.
func handleManagementConn(conn net.Conn, profile ProfileConfig) error {
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, ">PASSWORD:Need 'Auth'") {
			continue
		}
		totpCode := ""
		if profile.TOTPSecret != "" {
			code, err := generateTOTP(profile.TOTPSecret)
			if err != nil {
				slog.Error("vpn: generate totp for management auth", "error", err)
				continue
			}
			totpCode = code
		}
		if _, err := fmt.Fprintf(conn, "username \"Auth\" %s\npassword \"Auth\" %s%s\n",
			profile.Username, profile.Password, totpCode); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// generateTOTPAtTime generates a 6-digit RFC 6238 TOTP code for the 30-second window
// containing t. secret is a base32-encoded key (case-insensitive, padding optional).
func generateTOTPAtTime(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		strings.ToUpper(strings.TrimRight(secret, "=")),
	)
	if err != nil {
		return "", fmt.Errorf("totp: decode secret: %w", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(t.Unix())/30)
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	h := mac.Sum(nil)
	offset := h[len(h)-1] & 0x0f
	code := (uint32(h[offset])&0x7f)<<24 |
		uint32(h[offset+1])<<16 |
		uint32(h[offset+2])<<8 |
		uint32(h[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// generateTOTP generates a 6-digit RFC 6238 TOTP code for the current 30-second window.
func generateTOTP(secret string) (string, error) {
	return generateTOTPAtTime(secret, time.Now())
}
