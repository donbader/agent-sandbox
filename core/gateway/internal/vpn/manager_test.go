package vpn

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_ConnectAll_Success(t *testing.T) {
	var calls atomic.Int32
	profiles := map[string]ProfileConfig{
		"alpha": {Type: "openvpn", ConfigB64: "dGVzdA=="},
		"beta":  {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(3),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(name string, _ ProfileConfig, _ string) error {
			calls.Add(1)
			return nil
		}),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	assert.Equal(t, int32(2), calls.Load())
	assert.True(t, mgr.AllConnected())

	statuses := mgr.Status()
	assert.Len(t, statuses, 2)
	for _, s := range statuses {
		assert.Equal(t, StateConnected, s.State)
		assert.Equal(t, 1, s.Attempts)
		assert.Empty(t, s.Error)
	}
}

func TestManager_ConnectAll_FailThenSucceed(t *testing.T) {
	attemptsByProfile := make(map[string]*atomic.Int32)
	attemptsByProfile["flaky"] = &atomic.Int32{}

	profiles := map[string]ProfileConfig{
		"flaky": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(3),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(name string, _ ProfileConfig, _ string) error {
			n := attemptsByProfile[name].Add(1)
			if n < 3 {
				return fmt.Errorf("connection timeout (attempt %d)", n)
			}
			return nil
		}),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	assert.True(t, mgr.AllConnected())
	statuses := mgr.Status()
	require.Len(t, statuses, 1)
	assert.Equal(t, StateConnected, statuses[0].State)
	assert.Equal(t, 3, statuses[0].Attempts)
}

func TestManager_ConnectAll_ExhaustsRetries(t *testing.T) {
	profiles := map[string]ProfileConfig{
		"dead": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(2),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			return fmt.Errorf("server unreachable")
		}),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	assert.False(t, mgr.AllConnected())
	statuses := mgr.Status()
	require.Len(t, statuses, 1)
	assert.Equal(t, StateFailed, statuses[0].State)
	assert.Equal(t, 2, statuses[0].Attempts)
	assert.Contains(t, statuses[0].Error, "server unreachable")
}

func TestManager_Reconnect_Success(t *testing.T) {
	var calls atomic.Int32
	profiles := map[string]ProfileConfig{
		"vpn1": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(2),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			calls.Add(1)
			return nil
		}),
	)

	// Initial connect
	mgr.ConnectAll()
	<-mgr.Ready()
	assert.True(t, mgr.AllConnected())

	// Reconnect
	err := mgr.Reconnect("vpn1")
	require.NoError(t, err)

	// Wait for reconnect to complete
	time.Sleep(50 * time.Millisecond)

	assert.True(t, mgr.AllConnected())
	// 1 initial + 1 reconnect = at least 2 calls
	assert.GreaterOrEqual(t, calls.Load(), int32(2))
}

func TestManager_Reconnect_UnknownProfile(t *testing.T) {
	profiles := map[string]ProfileConfig{
		"known": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error { return nil }),
	)

	err := mgr.Reconnect("unknown-profile")
	assert.ErrorContains(t, err, "not found")
}

func TestManager_ReconnectAll(t *testing.T) {
	var calls atomic.Int32
	profiles := map[string]ProfileConfig{
		"a": {Type: "openvpn", ConfigB64: "dGVzdA=="},
		"b": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			calls.Add(1)
			return nil
		}),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	// Reset and reconnect all
	mgr.ReconnectAll()
	<-mgr.Ready()

	assert.True(t, mgr.AllConnected())
	// 2 initial + 2 reconnect = 4
	assert.Equal(t, int32(4), calls.Load())
}

func TestManager_Dialer_ReturnsNilWhenDisconnected(t *testing.T) {
	profiles := map[string]ProfileConfig{
		"failing": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			return fmt.Errorf("nope")
		}),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	assert.Nil(t, mgr.Dialer("failing"))
}

func TestManager_Dialer_ReturnsDialerWhenConnected(t *testing.T) {
	profiles := map[string]ProfileConfig{
		"good": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error { return nil }),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	d := mgr.Dialer("good")
	assert.NotNil(t, d)
}

func TestManager_Dialer_UnknownProfile(t *testing.T) {
	mgr := NewManager(map[string]ProfileConfig{},
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error { return nil }),
	)
	assert.Nil(t, mgr.Dialer("nonexistent"))
}

func TestManager_HasProfile(t *testing.T) {
	profiles := map[string]ProfileConfig{
		"exists": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}
	mgr := NewManager(profiles,
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error { return nil }),
	)
	assert.True(t, mgr.HasProfile("exists"))
	assert.False(t, mgr.HasProfile("nope"))
}

func TestManager_StatusJSON(t *testing.T) {
	profiles := map[string]ProfileConfig{
		"vpn1": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error { return nil }),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	data, err := mgr.StatusJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"state":"connected"`)
	assert.Contains(t, string(data), `"profile":"vpn1"`)
}

func TestManager_SortedInterfaceAssignment(t *testing.T) {
	// Profiles should get tun interfaces in alphabetical order by name.
	profiles := map[string]ProfileConfig{
		"zebra": {Type: "openvpn", ConfigB64: "dGVzdA=="},
		"alpha": {Type: "openvpn", ConfigB64: "dGVzdA=="},
		"mike":  {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	var mu sync.Mutex
	ifaceByName := make(map[string]string)
	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithStartFunc(func(name string, _ ProfileConfig, iface string) error {
			mu.Lock()
			ifaceByName[name] = iface
			mu.Unlock()
			return nil
		}),
	)

	mgr.ConnectAll()
	<-mgr.Ready()

	// Sorted order: alpha=tun0, mike=tun1, zebra=tun2
	assert.Equal(t, "tun0", ifaceByName["alpha"])
	assert.Equal(t, "tun1", ifaceByName["mike"])
	assert.Equal(t, "tun2", ifaceByName["zebra"])
}

// TestManager_HealthWatcher_TriggersReconnect verifies that the health watcher
// triggers a Reconnect when the iface check fails consecutiveThreshold times.
func TestManager_HealthWatcher_TriggersReconnect(t *testing.T) {
	var startCalls atomic.Int32
	profiles := map[string]ProfileConfig{
		"vpn1": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	var ifaceUp atomic.Bool
	ifaceUp.Store(true)

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithHealthInterval(10*time.Millisecond),
		WithHealthThreshold(2),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			startCalls.Add(1)
			return nil
		}),
		WithIfaceCheck(func(_ string) bool { return ifaceUp.Load() }),
	)
	defer mgr.Shutdown()

	mgr.ConnectAll()
	<-mgr.Ready()
	require.True(t, mgr.AllConnected())
	require.Equal(t, int32(1), startCalls.Load())

	// Simulate iface going down.
	ifaceUp.Store(false)

	// After threshold (2) consecutive failures the watcher calls Reconnect.
	// Reconnect → killTunnel has a 1s sleep, so allow 3s total.
	require.Eventually(t, func() bool {
		return startCalls.Load() >= 2
	}, 3*time.Second, time.Millisecond, "health watcher should have triggered reconnect")
}

// TestManager_HealthWatcher_NoReconnectWhenIfaceUp verifies that the watcher
// does NOT trigger a reconnect when the interface stays healthy.
func TestManager_HealthWatcher_NoReconnectWhenIfaceUp(t *testing.T) {
	var startCalls atomic.Int32
	profiles := map[string]ProfileConfig{
		"vpn1": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithHealthInterval(10*time.Millisecond),
		WithHealthThreshold(2),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			startCalls.Add(1)
			return nil
		}),
		WithIfaceCheck(func(_ string) bool { return true }),
	)
	defer mgr.Shutdown()

	mgr.ConnectAll()
	<-mgr.Ready()
	require.True(t, mgr.AllConnected())

	// Let the watcher run for several intervals.
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), startCalls.Load(), "health watcher should not reconnect when iface is healthy")
}

// TestManager_HealthWatcher_ConsecutiveThreshold verifies that a single failed
// poll does NOT trigger a reconnect — only sustained failures do.
func TestManager_HealthWatcher_ConsecutiveThreshold(t *testing.T) {
	var startCalls atomic.Int32
	profiles := map[string]ProfileConfig{
		"vpn1": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	var checkMu sync.Mutex
	checkCount := 0
	// Down on checks 2–3, up on check 4 (recovers before threshold=4).
	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithHealthInterval(10*time.Millisecond),
		WithHealthThreshold(4),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			startCalls.Add(1)
			return nil
		}),
		WithIfaceCheck(func(_ string) bool {
			checkMu.Lock()
			defer checkMu.Unlock()
			checkCount++
			return checkCount != 2 && checkCount != 3
		}),
	)
	defer mgr.Shutdown()

	mgr.ConnectAll()
	<-mgr.Ready()
	require.True(t, mgr.AllConnected())

	// Let the watcher run through several poll cycles.
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int32(1), startCalls.Load(), "transient iface-down should not trigger reconnect")
}

// TestManager_Shutdown_StopsHealthWatcher verifies that Shutdown stops the health
// watcher and no further reconnects are triggered after shutdown.
func TestManager_Shutdown_StopsHealthWatcher(t *testing.T) {
	var startCalls atomic.Int32
	profiles := map[string]ProfileConfig{
		"vpn1": {Type: "openvpn", ConfigB64: "dGVzdA=="},
	}

	var ifaceUp atomic.Bool
	ifaceUp.Store(true)

	mgr := NewManager(profiles,
		WithMaxRetries(1),
		WithRetryBackoff(1*time.Millisecond),
		WithHealthInterval(10*time.Millisecond),
		WithHealthThreshold(1),
		WithStartFunc(func(_ string, _ ProfileConfig, _ string) error {
			startCalls.Add(1)
			return nil
		}),
		WithIfaceCheck(func(_ string) bool { return ifaceUp.Load() }),
	)

	mgr.ConnectAll()
	<-mgr.Ready()
	require.True(t, mgr.AllConnected())

	// Shut down before taking the iface down.
	mgr.Shutdown()

	// Now simulate iface failure — watcher is stopped so no reconnect should fire.
	ifaceUp.Store(false)
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), startCalls.Load(), "no reconnects should happen after Shutdown")
}
