//go:build !windows

package mdns

import (
	"net"
	"syscall"
)

// multicastLoop lets this computer see its own answers (Go turns loopback off), so a browser on the
// computer running HomeOwl can open the names too.
func multicastLoop(c *net.UDPConn, v6 bool) {
	if rc, err := c.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) {
			if v6 {
				_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_LOOP, 1)
			} else {
				_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
			}
		})
	}
}
