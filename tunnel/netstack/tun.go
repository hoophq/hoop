package netstack

import "io"

// RouteConfig carries the per-session addressing the platform route
// helpers need to wire the host kernel to the TUN device. Both families
// are required now that the tunnel is dual-stack: the v6 prefix/host pair
// mirrors the original design, and the v4 pair (a 100.64.0.0/10 CGNAT /16
// + host address) is what makes connections reachable on macOS, where
// getaddrinfo suppresses AAAA (see the addressing package).
//
// Addresses are plain strings because the platform helpers shell out to
// ip/ifconfig/route, which take strings anyway, and the caller already
// has them as strings from the allocator.
type RouteConfig struct {
	Device string // TUN device name (e.g. tun0 / utun9)

	Prefix   string // IPv6 /48, e.g. "fd5a:1b2c:3d4e::/48"
	HostAddr string // IPv6 host address assigned to the interface (::2)

	PrefixV4   string // IPv4 /16, e.g. "100.83.4.0/16"
	HostAddrV4 string // IPv4 host address assigned to the interface (x.x.0.2)
}

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
