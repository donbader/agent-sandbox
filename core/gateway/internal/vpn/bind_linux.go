//go:build linux

package vpn

import (
	"net"
	"syscall"
	"time"
)

// newBoundDialer returns a net.Dialer whose TCP sockets are pinned to iface
// via SO_BINDTODEVICE. Connections made with this dialer are forced through
// the named network interface regardless of the default routing table.
func newBoundDialer(iface string, timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				if err := syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface); err != nil {
					// Non-fatal: log but continue. The connection will use the default route.
					// This can happen briefly while the tun interface is still initialising.
					_ = err
				}
			})
		},
	}
}
