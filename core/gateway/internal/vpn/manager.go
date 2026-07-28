// Package vpn — manager.go provides a Manager that tracks tunnel lifecycle,
// supports non-blocking startup with retries, and exposes reconnect capability.
package vpn

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"sync"
	"time"
)

// State represents the current status of a VPN tunnel.
type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateFailed       State = "failed"
)

const (
	// defaultMaxRetries is the number of times to retry a failed tunnel startup.
	defaultMaxRetries = 3

	// defaultRetryBackoff is the base delay between retry attempts.
	defaultRetryBackoff = 5 * time.Second
)

// TunnelStatus is the JSON-serializable status of a single VPN tunnel.
type TunnelStatus struct {
	Profile   string    `json:"profile"`
	Interface string    `json:"interface"`
	State     State     `json:"state"`
	Error     string    `json:"error,omitempty"`
	Since     time.Time `json:"since"`
	Attempts  int       `json:"attempts"`
}

// tunnel is internal state for a single VPN profile.
type tunnel struct {
	mu        sync.Mutex
	profile   ProfileConfig
	name      string
	iface     string
	state     State
	lastErr   string
	since     time.Time
	attempts  int
	dialer    *net.Dialer
}

// Manager coordinates multiple VPN tunnels with lifecycle management.
type Manager struct {
	tunnels    map[string]*tunnel
	maxRetries int
	backoff    time.Duration
	mu         sync.Mutex   // protects ready and once
	ready      chan struct{} // closed when initial connection attempts complete
	once       sync.Once
	startFunc  func(name string, profile ProfileConfig, tunIface string) error // injectable for testing
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithMaxRetries sets the maximum number of retry attempts per tunnel.
func WithMaxRetries(n int) ManagerOption {
	return func(m *Manager) { m.maxRetries = n }
}

// WithRetryBackoff sets the base delay between retries.
func WithRetryBackoff(d time.Duration) ManagerOption {
	return func(m *Manager) { m.backoff = d }
}

// WithStartFunc overrides the tunnel start function (for testing).
func WithStartFunc(f func(name string, profile ProfileConfig, tunIface string) error) ManagerOption {
	return func(m *Manager) { m.startFunc = f }
}

// NewManager creates a Manager for the given profiles. Tunnels are assigned
// tun interfaces in sorted name order (tun0, tun1, …) for determinism.
func NewManager(profiles map[string]ProfileConfig, opts ...ManagerOption) *Manager {
	m := &Manager{
		tunnels:    make(map[string]*tunnel, len(profiles)),
		maxRetries: defaultMaxRetries,
		backoff:    defaultRetryBackoff,
		ready:      make(chan struct{}),
		startFunc:  startTunnel,
	}
	for _, opt := range opts {
		opt(m)
	}

	// Assign tun interfaces in sorted order for stability.
	names := sortedKeys(profiles)
	for i, name := range names {
		m.tunnels[name] = &tunnel{
			profile: profiles[name],
			name:    name,
			iface:   fmt.Sprintf("tun%d", i),
			state:   StateDisconnected,
			since:   time.Now(),
		}
	}
	return m
}

// ConnectAll starts all tunnels asynchronously. Each tunnel retries up to
// maxRetries times with backoff. The method returns immediately; callers can
// wait on Ready() if they need to block until the initial round completes.
func (m *Manager) ConnectAll() {
	var wg sync.WaitGroup
	for _, tun := range m.tunnels {
		wg.Add(1)
		go func(t *tunnel) {
			defer wg.Done()
			m.connectWithRetry(t)
		}(tun)
	}
	go func() {
		wg.Wait()
		m.mu.Lock()
		m.once.Do(func() { close(m.ready) })
		m.mu.Unlock()
	}()
}

// Ready returns a channel that is closed when all initial connection attempts
// (including retries) have completed. Does NOT guarantee all tunnels are connected.
func (m *Manager) Ready() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready
}

// connectWithRetry attempts to start a tunnel up to maxRetries times.
func (m *Manager) connectWithRetry(t *tunnel) {
	t.mu.Lock()
	t.state = StateConnecting
	t.since = time.Now()
	t.attempts = 0
	t.lastErr = ""
	t.mu.Unlock()

	for attempt := 1; attempt <= m.maxRetries; attempt++ {
		t.mu.Lock()
		t.attempts = attempt
		t.mu.Unlock()

		err := m.startFunc(t.name, t.profile, t.iface)
		if err == nil {
			t.mu.Lock()
			t.state = StateConnected
			t.since = time.Now()
			t.dialer = newBoundDialer(t.iface, 15*time.Second)
			t.mu.Unlock()
			slog.Info("vpn tunnel connected", "profile", t.name, "iface", t.iface, "attempts", attempt)
			return
		}

		slog.Warn("vpn tunnel attempt failed",
			"profile", t.name, "iface", t.iface,
			"attempt", attempt, "max", m.maxRetries, "error", err)

		t.mu.Lock()
		t.lastErr = err.Error()
		t.mu.Unlock()

		if attempt < m.maxRetries {
			backoff := m.backoff * time.Duration(attempt)
			time.Sleep(backoff)
		}
	}

	t.mu.Lock()
	t.state = StateFailed
	t.since = time.Now()
	t.mu.Unlock()
	slog.Error("vpn tunnel failed after retries", "profile", t.name, "attempts", m.maxRetries, "last_error", t.lastErr)
}

// Reconnect kills any existing openvpn process for the named profile and
// restarts the tunnel with fresh retries. Returns an error only if the profile
// doesn't exist. Connection result is communicated via Status().
func (m *Manager) Reconnect(profileName string) error {
	t, ok := m.tunnels[profileName]
	if !ok {
		return fmt.Errorf("vpn profile %q not found", profileName)
	}

	t.mu.Lock()
	prevState := t.state
	t.mu.Unlock()

	// Kill existing openvpn daemon if running.
	if prevState == StateConnected || prevState == StateConnecting {
		m.killTunnel(t)
	}

	go m.connectWithRetry(t)
	return nil
}

// ReconnectAll kills and restarts all tunnels.
func (m *Manager) ReconnectAll() {
	for _, t := range m.tunnels {
		t.mu.Lock()
		prevState := t.state
		t.mu.Unlock()
		if prevState == StateConnected || prevState == StateConnecting {
			m.killTunnel(t)
		}
	}
	// Reset the ready channel for a new round.
	m.mu.Lock()
	m.once = sync.Once{}
	m.ready = make(chan struct{})
	m.mu.Unlock()
	m.ConnectAll()
}

// Status returns the current status of all tunnels.
func (m *Manager) Status() []TunnelStatus {
	statuses := make([]TunnelStatus, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		t.mu.Lock()
		statuses = append(statuses, TunnelStatus{
			Profile:   t.name,
			Interface: t.iface,
			State:     t.state,
			Error:     t.lastErr,
			Since:     t.since,
			Attempts:  t.attempts,
		})
		t.mu.Unlock()
	}
	return statuses
}

// StatusJSON returns the JSON-encoded status of all tunnels.
func (m *Manager) StatusJSON() ([]byte, error) {
	return json.Marshal(m.Status())
}

// AllConnected returns true if every tunnel is in the Connected state.
func (m *Manager) AllConnected() bool {
	for _, t := range m.tunnels {
		t.mu.Lock()
		s := t.state
		t.mu.Unlock()
		if s != StateConnected {
			return false
		}
	}
	return true
}

// Dialer returns the bound net.Dialer for the named profile, or nil if the
// tunnel is not connected. Callers should check Status() for the reason.
func (m *Manager) Dialer(profileName string) *net.Dialer {
	t, ok := m.tunnels[profileName]
	if !ok {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != StateConnected {
		return nil
	}
	return t.dialer
}

// HasProfile returns true if the manager knows about the named profile.
func (m *Manager) HasProfile(profileName string) bool {
	_, ok := m.tunnels[profileName]
	return ok
}

// killTunnel sends SIGTERM to the openvpn process associated with the tunnel's
// interface. Best-effort: if pkill fails (e.g., process already exited), we log
// and continue.
func (m *Manager) killTunnel(t *tunnel) {
	t.mu.Lock()
	t.state = StateDisconnected
	t.dialer = nil
	t.mu.Unlock()

	// pkill by matching the --dev flag since each tunnel has a unique tun interface.
	cmd := exec.Command("pkill", "-f", fmt.Sprintf("openvpn.*--dev %s", t.iface))
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Debug("pkill openvpn (may already be stopped)", "iface", t.iface, "error", err, "output", string(out))
	}
	// Wait briefly for the process to die and interface to disappear.
	time.Sleep(1 * time.Second)
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys(m map[string]ProfileConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Use sort from the standard library (already imported in vpn.go via the
	// same package). Import it here too for self-containment.
	sortStrings(keys)
	return keys
}

// sortStrings sorts a slice of strings in place.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
