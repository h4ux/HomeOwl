//go:build !windows

package mdns

import (
	"net"
	"syscall"
)

// multicastLoop lets this computer see its own answers (Go turns loopback off), so a browser on the
// computer running HomeOwl can open the names too.
func multicastLoop(c *net.UDPConn) {
	if rc, err := c.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { _ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1) })
	}
}
