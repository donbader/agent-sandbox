//go:build !linux

package vpn

import (
	"net"
	"time"
)

// newBoundDialer returns a standard net.Dialer on non-Linux platforms.
// SO_BINDTODEVICE is Linux-only; in tests and non-Linux builds a plain
// dialer is returned. VPN tunnels are only functional on Linux.
func newBoundDialer(_ string, timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout}
}
