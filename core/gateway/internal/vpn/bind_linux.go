//go:build linux

package vpn

import (
	"log/slog"
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
					// Non-fatal, but important to surface: when SO_BINDTODEVICE fails
					// the connection silently falls back to the default route, bypassing
					// the VPN. This can happen if CAP_NET_RAW is unavailable.
					slog.Warn("SO_BINDTODEVICE failed, connection will use default route",
						"iface", iface, "err", err)
				}
			})
		},
	}
}
