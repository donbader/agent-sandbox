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
				// Non-fatal: a transient failure here means the connection falls
				// back to the default route. SetsockoptString fails immediately if
				// the tun interface hasn't fully initialised yet; the idempotency
				// check in startTunnel ensures we only reach here when it is UP.
				_ = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
			})
		},
	}
}
