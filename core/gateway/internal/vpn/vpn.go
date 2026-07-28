// Package vpn manages per-profile OpenVPN tunnels for the gateway.
// Each profile starts an openvpn daemon, waits for the tun interface
// to come up, then creates a net.Dialer bound to that interface via
// SO_BINDTODEVICE so specific upstream connections route through the tunnel.
package vpn

import (
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

	// Idempotency: if the interface is already up (e.g. another code path already
	// started this tunnel), just return without launching a second daemon.
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

	// Write to a temp file.
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

	// Build auth credentials if provided.
	var authPath string
	if profile.Username != "" || profile.Password != "" || profile.TOTPSecret != "" {
		totpCode := ""
		if profile.TOTPSecret != "" {
			code, err := generateTOTP(profile.TOTPSecret)
			if err != nil {
				_ = os.Remove(cfgPath)
				return fmt.Errorf("generate totp: %w", err)
			}
			totpCode = code
		}
		af, err := os.CreateTemp("", fmt.Sprintf("vpn-%s-*.auth", name))
		if err != nil {
			_ = os.Remove(cfgPath)
			return fmt.Errorf("create auth file: %w", err)
		}
		authPath = af.Name()
		_, err = fmt.Fprintf(af, "%s\n%s%s\n", profile.Username, profile.Password, totpCode)
		af.Close()
		if err != nil {
			_ = os.Remove(cfgPath)
			_ = os.Remove(authPath)
			return fmt.Errorf("write auth file: %w", err)
		}
	}

	// Launch openvpn as a daemon. We override --dev so each profile gets its
	// own deterministic tun device, regardless of what the .ovpn file specifies.
	logPath := fmt.Sprintf("/tmp/vpn-%s.log", name)
	args := []string{
		"--config", cfgPath,
		"--dev", tunIface,
		"--daemon",
		"--log", logPath,
		"--script-security", "2",
	}
	if authPath != "" {
		args = append(args, "--auth-user-pass", authPath)
	}
	cmd := exec.Command("openvpn", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(cfgPath)
		if authPath != "" {
			_ = os.Remove(authPath)
		}
		return fmt.Errorf("openvpn start failed: %w\n%s", err, out)
	}

	// Poll until the tun interface appears and is UP. The config file is deleted
	// once the interface comes up (openvpn has read it by then) so credentials
	// don't persist on disk longer than necessary.
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if ifaceIsUp(tunIface) {
			_ = os.Remove(cfgPath) // openvpn has fully read the config, safe to delete
			if authPath != "" {
				_ = os.Remove(authPath)
			}
			slog.Info("vpn tunnel started", "profile", name, "iface", tunIface)
			return nil
		}
		time.Sleep(pollInterval)
	}

	_ = os.Remove(cfgPath) // clean up even on timeout
	if authPath != "" {
		_ = os.Remove(authPath)
	}
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
