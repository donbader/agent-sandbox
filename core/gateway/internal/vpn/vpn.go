// Package vpn manages per-profile OpenVPN tunnels for the gateway.
// Each profile starts an openvpn daemon, waits for the tun interface
// to come up, then creates a net.Dialer bound to that interface via
// SO_BINDTODEVICE so specific upstream connections route through the tunnel.
package vpn

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"time"
)

const (
	// startupTimeout is how long we wait for a tun interface to appear after
	// launching openvpn before giving up.
	startupTimeout = 60 * time.Second

	// pollInterval is how often we poll for the tun interface.
	pollInterval = 500 * time.Millisecond

	// dialTimeout is the per-connection timeout for VPN-bound dials.
	dialTimeout = 15 * time.Second
)

// ProfileConfig is the VPN profile configuration passed from the gateway config.
type ProfileConfig struct {
	Type      string // "openvpn"
	ConfigB64 string // base64-encoded .ovpn file content
}

// Manager holds the per-profile dialers created after VPN tunnels come up.
type Manager struct {
	dialers map[string]*net.Dialer // profile name → bound dialer
}

// DialerFor returns the VPN-bound dialer for the given profile name.
// Returns nil when the profile is unknown or the VPN failed to start.
func (m *Manager) DialerFor(profile string) *net.Dialer {
	if m == nil {
		return nil
	}
	return m.dialers[profile]
}

// New starts VPN tunnels for all given profiles and returns a Manager with
// one bound dialer per profile. tun interfaces are assigned in order:
// tun0 for the first profile, tun1 for the second, and so on.
//
// Returns an error if any tunnel fails to start or the tun interface does
// not appear within startupTimeout.
func New(profiles map[string]ProfileConfig) (*Manager, error) {
	if len(profiles) == 0 {
		return &Manager{dialers: map[string]*net.Dialer{}}, nil
	}

	m := &Manager{dialers: make(map[string]*net.Dialer, len(profiles))}

	// Assign a deterministic tun index to each profile (sorted by name for
	// reproducibility across restarts).
	names := sortedKeys(profiles)
	for i, name := range names {
		profile := profiles[name]
		tunIface := fmt.Sprintf("tun%d", i)
		if err := startTunnel(name, profile, tunIface); err != nil {
			return nil, fmt.Errorf("vpn profile %q: %w", name, err)
		}
		slog.Info("vpn tunnel started", "profile", name, "iface", tunIface)
		m.dialers[name] = newBoundDialer(tunIface, dialTimeout)
	}
	return m, nil
}

// StartTunnel decodes the profile config, writes it to a temp file, launches
// an openvpn daemon pinned to the given tun device, and waits for the
// interface to come up.
func StartTunnel(name string, profile ProfileConfig, tunIface string) error {
	return startTunnel(name, profile, tunIface)
}

// NewBoundDialer returns a Dialer whose TCP sockets are bound to iface.
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

	// Launch openvpn as a daemon. We override --dev so each profile gets its
	// own deterministic tun device, regardless of what the .ovpn file specifies.
	logPath := fmt.Sprintf("/tmp/vpn-%s.log", name)
	cmd := exec.Command("openvpn",
		"--config", cfgPath,
		"--dev", tunIface,
		"--daemon",
		"--log", logPath,
		"--script-security", "2",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("openvpn start failed: %w\n%s", err, out)
	}

	// Poll until the tun interface appears and is UP.
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if ifaceIsUp(tunIface) {
			return nil
		}
		time.Sleep(pollInterval)
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

// sortedKeys returns map keys in alphabetical order.
func sortedKeys(m map[string]ProfileConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — profile count is tiny.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
