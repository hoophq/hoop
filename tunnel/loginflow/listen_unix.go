//go:build linux || darwin || freebsd || openbsd || netbsd

package loginflow

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// applyReuseAddr sets SO_REUSEADDR on the unbound socket. See
// listen.go for the rationale.
//
// We use golang.org/x/sys/unix instead of syscall.SetsockoptInt
// because the unix constants are kept current; on macOS the
// SO_REUSEPORT vs SO_REUSEADDR semantics drifted historically and
// the x/sys package shields us from any of that complexity.
func applyReuseAddr(c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
