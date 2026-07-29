// Package vpn — manager.go provides a Manager that tracks tunnel lifecycle,
// supports non-blocking startup with retries, health watching, and exposes
// reconnect capability.
package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"sort"
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

	// defaultHealthInterval is how often the health watcher polls the tun interface.
	defaultHealthInterval = 30 * time.Second

	// defaultHealthThreshold is the number of consecutive failed health checks
	// required before the Manager triggers a reconnect. This provides a grace
	// window for openvpn's own --ping-restart (which transiently takes tun down).
	defaultHealthThreshold = 2
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
	mu      sync.Mutex
	profile ProfileConfig
	name    string
	iface   string
	state   State
	lastErr string
	since   time.Time
	attempts int
	dialer  *net.Dialer
	cancel  context.CancelFunc // cancels in-flight retry loop + health watcher
}

// Manager coordinates multiple VPN tunnels with lifecycle management,
// automatic health watching, and graceful shutdown.
type Manager struct {
	tunnels         map[string]*tunnel
	names           []string      // sorted profile names for deterministic iteration
	maxRetries      int
	backoff         time.Duration
	healthInterval  time.Duration
	healthThreshold int
	ifaceCheck      func(iface string) bool // injectable for testing; default: ifaceIsUp
	startFunc       func(name string, profile ProfileConfig, tunIface string) error
	mu              sync.Mutex   // protects ready, once, and generation
	ready           chan struct{} // closed when initial connection attempts complete
	once            sync.Once
	generation      uint64 // incremented on each ConnectAll to prevent stale closures
	rootCtx         context.Context
	rootCancel      context.CancelFunc
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithMaxRetries sets the maximum number of retry attempts per tunnel.
func WithMaxRetries(n int) ManagerOption {
	return func(m *Manager) { m.maxRetries = n }
}

// WithRetryBackoff sets the base delay between retry attempts.
func WithRetryBackoff(d time.Duration) ManagerOption {
	return func(m *Manager) { m.backoff = d }
}

// WithStartFunc overrides the tunnel start function (for testing).
func WithStartFunc(f func(name string, profile ProfileConfig, tunIface string) error) ManagerOption {
	return func(m *Manager) { m.startFunc = f }
}

// WithIfaceCheck overrides the interface liveness check (for testing).
// The default is ifaceIsUp.
func WithIfaceCheck(f func(iface string) bool) ManagerOption {
	return func(m *Manager) { m.ifaceCheck = f }
}

// WithHealthInterval sets how often the health watcher polls each tunnel's
// tun interface. Default is 30 seconds.
func WithHealthInterval(d time.Duration) ManagerOption {
	return func(m *Manager) { m.healthInterval = d }
}

// WithHealthThreshold sets the number of consecutive failed health polls
// before a reconnect is triggered. Default is 2, which gives a grace window
// for openvpn's own --ping-restart to recover without interference.
func WithHealthThreshold(n int) ManagerOption {
	return func(m *Manager) { m.healthThreshold = n }
}

// NewManager creates a Manager for the given profiles. Tunnels are assigned
// tun interfaces in sorted name order (tun0, tun1, …) for determinism.
func NewManager(profiles map[string]ProfileConfig, opts ...ManagerOption) *Manager {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	m := &Manager{
		tunnels:         make(map[string]*tunnel, len(profiles)),
		maxRetries:      defaultMaxRetries,
		backoff:         defaultRetryBackoff,
		healthInterval:  defaultHealthInterval,
		healthThreshold: defaultHealthThreshold,
		ifaceCheck:      ifaceIsUp,
		startFunc:       startTunnel,
		ready:           make(chan struct{}),
		rootCtx:         rootCtx,
		rootCancel:      rootCancel,
	}
	for _, opt := range opts {
		opt(m)
	}

	// Assign tun interfaces in sorted order for stability.
	m.names = sortedKeys(profiles)
	for i, name := range m.names {
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
	m.mu.Lock()
	m.generation++
	gen := m.generation
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, tun := range m.tunnels {
		wg.Add(1)
		go func(t *tunnel) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(m.rootCtx)
			t.mu.Lock()
			// Cancel any previous retry loop / watcher for this tunnel.
			if t.cancel != nil {
				t.cancel()
			}
			t.cancel = cancel
			t.mu.Unlock()
			m.connectWithRetry(ctx, t)
		}(tun)
	}
	go func() {
		wg.Wait()
		m.mu.Lock()
		// Only close ready if this is still the current generation.
		if gen == m.generation {
			m.once.Do(func() { close(m.ready) })
		}
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
// On success it spawns a health watcher on the same ctx.
// It respects ctx cancellation between retry attempts.
func (m *Manager) connectWithRetry(ctx context.Context, t *tunnel) {
	t.mu.Lock()
	t.state = StateConnecting
	t.since = time.Now()
	t.attempts = 0
	t.lastErr = ""
	t.mu.Unlock()

	for attempt := 1; attempt <= m.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return
		}

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
			// Start health watcher on the same ctx — cancelled when tunnel is
			// killed or Manager is shut down.
			go m.watchTunnel(ctx, t)
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
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
		}
	}

	// Only mark as failed if we weren't cancelled.
	if ctx.Err() != nil {
		return
	}
	t.mu.Lock()
	t.state = StateFailed
	t.since = time.Now()
	t.mu.Unlock()
	slog.Error("vpn tunnel failed after retries", "profile", t.name, "attempts", m.maxRetries, "last_error", t.lastErr)
}

// watchTunnel polls the tun interface at healthInterval. After healthThreshold
// consecutive failed polls it triggers a Reconnect. The watcher exits when ctx
// is cancelled (i.e. when the tunnel is killed or Shutdown is called).
//
// Consecutive-failure threshold guards against transient tun drops during
// openvpn's own --ping-restart cycle, which would otherwise fight the daemon's
// own recovery attempt.
//
// On threshold breach: nil the dialer immediately so new connections fast-fail,
// but leave state as StateConnected so Reconnect's prevState check correctly
// invokes killTunnel on the dead/half-dead openvpn process.
func (m *Manager) watchTunnel(ctx context.Context, t *tunnel) {
	ticker := time.NewTicker(m.healthInterval)
	defer ticker.Stop()

	consecutive := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.mu.Lock()
			state := t.state
			t.mu.Unlock()

			// Only watch tunnels that are connected; skip if already being
			// reconnected/failed.
			if state != StateConnected {
				consecutive = 0
				continue
			}

			if m.ifaceCheck(t.iface) {
				consecutive = 0
				continue
			}

			consecutive++
			slog.Warn("vpn health check: iface down",
				"profile", t.name, "iface", t.iface,
				"consecutive", consecutive, "threshold", m.healthThreshold)

			if consecutive < m.healthThreshold {
				continue
			}

			// Threshold reached. Nil the dialer so in-flight and new connections
			// fail fast. Keep state=StateConnected so Reconnect will kill the
			// openvpn process before restarting.
			slog.Warn("vpn health threshold reached, triggering reconnect",
				"profile", t.name, "iface", t.iface)
			t.mu.Lock()
			t.dialer = nil
			t.mu.Unlock()
			consecutive = 0

			// Reconnect cancels this ctx, so the watcher exits after the call
			// returns and the for-loop's next iteration hits ctx.Done().
			_ = m.Reconnect(t.name)
		}
	}
}

// Reconnect kills any existing openvpn process for the named profile and
// restarts the tunnel with fresh retries. Returns an error only if the profile
// doesn't exist. Connection result is communicated via Status().
func (m *Manager) Reconnect(profileName string) error {
	t, ok := m.tunnels[profileName]
	if !ok {
		return fmt.Errorf("vpn profile %q not found", profileName)
	}

	// Cancel any in-flight retry loop / watcher, then kill the tunnel.
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
	}
	prevState := t.state
	t.mu.Unlock()

	if prevState == StateConnected || prevState == StateConnecting {
		m.killTunnel(t)
	}

	// Start a new retry loop with a fresh context derived from root.
	ctx, cancel := context.WithCancel(m.rootCtx)
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
	go m.connectWithRetry(ctx, t)
	return nil
}

// ReconnectAll kills and restarts all tunnels.
func (m *Manager) ReconnectAll() {
	for _, t := range m.tunnels {
		t.mu.Lock()
		if t.cancel != nil {
			t.cancel()
		}
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

// Shutdown cancels the root context, stopping all health watchers and
// in-flight retry loops. It also cancels every management goroutine that was
// spawned by startTunnel (those derive from context.Background(), not rootCtx,
// so they must be stopped explicitly via the package-level mgmtCancels map).
// Shutdown does not kill running openvpn processes.
func (m *Manager) Shutdown() {
	m.rootCancel()
	for _, t := range m.tunnels {
		if cancel, ok := mgmtCancels.LoadAndDelete(t.iface); ok {
			cancel.(context.CancelFunc)()
		}
	}
}

// Status returns the current status of all tunnels in deterministic order.
func (m *Manager) Status() []TunnelStatus {
	statuses := make([]TunnelStatus, 0, len(m.tunnels))
	for _, name := range m.names {
		t := m.tunnels[name]
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
	sort.Strings(keys)
	return keys
}
