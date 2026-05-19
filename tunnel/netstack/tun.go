package netstack

import "io"

// tunDevice is the OS-level virtual network device. The Linux implementation
// lives in stack_linux.go.
//
// Reads return whole IPv6 packets (one packet per Read). Writes send a whole
// IPv6 packet. We never split packets across calls.
type tunDevice interface {
	io.ReadWriteCloser
	// Name returns the kernel-assigned device name (e.g. "tun0"). Used for
	// route/DNS configuration outside the netstack package.
	Name() string
}
