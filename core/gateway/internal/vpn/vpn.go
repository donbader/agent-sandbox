// Package vpn manages per-profile OpenVPN tunnels for the gateway.
// Each profile starts an openvpn daemon, waits for the tun interface
// to come up, then creates a net.Dialer bound to that interface via
// SO_BINDTODEVICE so specific upstream connections route through the tunnel.
package vpn

import (
	"bufio"
	"context"
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
	"sync"
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
	Type       string // "openvpn"
	ConfigB64  string // base64-encoded .ovpn file content
	Username   string // openvpn auth username
	Password   string // openvpn static password (concatenated before TOTP code)
	TOTPSecret string // base32-encoded TOTP secret (RFC 6238); empty = no TOTP
}

// mgmtCancels tracks the cancel func for each active management goroutine,
// keyed by tun interface name (e.g. "tun0"). Prevents goroutine leaks when
// startTunnel is called again for the same interface (e.g. via Reconnect).
var mgmtCancels sync.Map // map[tunIface string]context.CancelFunc

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
	//
	// Cancel any previous management goroutine for this interface first to
	// prevent leaks. A defer+succeeded flag ensures the newly spawned goroutine
	// is also cancelled if startTunnel fails after spawning it.
	var mgmtCancel context.CancelFunc
	if profile.Username != "" || profile.Password != "" || profile.TOTPSecret != "" {
		port := tunManagementPort(tunIface)
		addr := fmt.Sprintf("127.0.0.1:%d", port)

		// Kill any existing management goroutine for this iface.
		if prev, ok := mgmtCancels.Load(tunIface); ok {
			prev.(context.CancelFunc)()
		}

		var mgmtCtx context.Context
		mgmtCtx, mgmtCancel = context.WithCancel(context.Background())
		mgmtCancels.Store(tunIface, mgmtCancel)

		args = append(args,
			"--management", "127.0.0.1", fmt.Sprintf("%d", port),
			"--management-query-passwords",
		)
		go serveManagement(mgmtCtx, addr, profile)
	}

	// Cancel the management goroutine if startTunnel returns an error.
	succeeded := false
	defer func() {
		if !succeeded && mgmtCancel != nil {
			mgmtCancel()
			mgmtCancels.Delete(tunIface)
		}
	}()

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
			succeeded = true
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
// credential queries. It runs in a goroutine for the lifetime of the tunnel,
// reconnecting automatically when the connection drops (e.g. on openvpn reconnect).
// On each auth query a fresh TOTP code is generated so every reconnect uses a
// valid, unexpired credential. The goroutine exits when ctx is cancelled.
func serveManagement(ctx context.Context, addr string, profile ProfileConfig) {
	const retryDelay = 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}
			continue
		}
		if err := handleManagementConn(ctx, conn, profile); err != nil {
			slog.Debug("vpn management connection closed", "addr", addr, "error", err)
		}
		_ = conn.Close()

		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}
}

// handleManagementConn reads from an OpenVPN management socket and responds to
// password queries. Returns when the connection is closed, an error occurs, or
// ctx is cancelled.
func handleManagementConn(ctx context.Context, conn net.Conn, profile ProfileConfig) error {
	// done is closed when this function returns, signalling the watcher goroutine
	// to exit. Without done, the goroutine would block on <-ctx.Done() for the
	// entire tunnel lifetime, accumulating one goroutine per openvpn reconnect.
	done := make(chan struct{})
	defer close(done)

	// Close conn when ctx is cancelled so the scanner unblocks.
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
			// handleManagementConn returned normally; nothing to do.
		}
	}()

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
	// Normalise: upper-case and strip padding — base32.DecodeString is strict.
	secret = strings.ToUpper(strings.TrimRight(secret, "="))
	// Re-pad to the next multiple of 8.
	if pad := len(secret) % 8; pad != 0 {
		secret += strings.Repeat("=", 8-pad)
	}
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("totp: decode secret: %w", err)
	}

	// RFC 4226 §5.3: counter = floor(unix / 30)
	counter := uint64(t.Unix()) / 30
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	h := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.4)
	offset := h[len(h)-1] & 0x0f
	code := (uint32(h[offset]&0x7f)<<24 |
		uint32(h[offset+1])<<16 |
		uint32(h[offset+2])<<8 |
		uint32(h[offset+3])) % 1_000_000

	return fmt.Sprintf("%06d", code), nil
}

// generateTOTP generates a 6-digit RFC 6238 TOTP code for the current 30-second window.
func generateTOTP(secret string) (string, error) {
	return generateTOTPAtTime(secret, time.Now())
}
