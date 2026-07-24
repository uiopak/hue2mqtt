//go:build !windows

package hue

import (
	"syscall"
)

func setReuseAddr(fd uintptr) {
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
