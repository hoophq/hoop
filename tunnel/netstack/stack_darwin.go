//go:build darwin

package netstack

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS TUN support is built on the stock `utun` device (RD-207). Unlike
// Linux's /dev/net/tun, there is no character device to open: a utun
// interface is created by opening a kernel-control socket
// (PF_SYSTEM / SYSPROTO_CONTROL) bound to the "com.apple.net.utun_control"
// control, then connecting it. The kernel materialises a utunN interface
// and we read/write IP packets over the socket fd.
//
// Two framing details differ from Linux and are handled entirely inside
// this file so that stack.go stays platform-neutral (its contract — one
// whole IPv6 packet per Read/Write — is preserved):
//
//   - Every datagram on a utun fd is prefixed by a 4-byte protocol
//     family in host… actually network byte order. For IPv6 that is
//     AF_INET6 (30). Read strips it; Write prepends it.
//
//   - The device name (utun3, utun7, …) is not chosen by us — the kernel
//     picks the lowest free unit when we connect with Unit=0. We read it
//     back with getsockopt(UTUN_OPT_IFNAME) so the caller can install
//     routes and DNS against the real name.
//
// These constants come from the macOS SDK headers and are NOT exported
// by golang.org/x/sys/unix, so we define them here:
//
//   - SYSPROTO_CONTROL  <sys/sys_domain.h>
//   - UTUN_CONTROL_NAME <net/if_utun.h>
//   - UTUN_OPT_IFNAME   <net/if_utun.h>  (getsockopt at SYSPROTO_CONTROL)
//
// AF_SYSTEM, AF_SYS_CONTROL, the SockaddrCtl type, IoctlCtlInfo, and
// GetsockoptString are all provided by x/sys/unix.
const (
	cSYSPROTO_CONTROL  = 2
	cUTUN_OPT_IFNAME   = 2
	cUTUN_CONTROL_NAME = "com.apple.net.utun_control"

	// utunHeaderLen is the size of the per-packet protocol-family
	// prefix utun puts in front of every datagram.
	utunHeaderLen = 4
)

// afInet6Prefix / afInetPrefix are the 4-byte big-endian protocol-family
// headers utun expects in front of every packet we write — AF_INET6 (30)
// for IPv6, AF_INET (2) for IPv4. Now that the tunnel is dual-stack, the
// family is chosen per packet from its IP version nibble. Precomputed
// once because we prepend one on every Write.
var (
	afInet6Prefix = func() [utunHeaderLen]byte {
		var b [utunHeaderLen]byte
		binary.BigEndian.PutUint32(b[:], uint32(unix.AF_INET6))
		return b
	}()
	afInetPrefix = func() [utunHeaderLen]byte {
		var b [utunHeaderLen]byte
		binary.BigEndian.PutUint32(b[:], uint32(unix.AF_INET))
		return b
	}()
)

// darwinTUN is the utun-backed tunDevice. It wraps the kernel-control
// socket fd in an *os.File so we get blocking Read/Write semantics and
// a Close that plays nicely with the gVisor packet loops in stack.go.
type darwinTUN struct {
	f    *os.File
	name string

	// rbuf / wbuf hold the 4-byte utun header plus a full MTU payload so
	// Read/Write never allocate on the hot path. They are only touched
	// from the single deviceToStack / stackToDevice goroutine each, so
	// no locking is required (the netstack package owns the device and
	// never issues concurrent reads or concurrent writes).
	rbuf []byte
	wbuf []byte
}

// openTUN creates a fresh utun interface and returns it as a tunDevice.
//
// requestedName is accepted for parity with the Linux signature but is
// only honoured when it matches the "utunN" shape — macOS lets us ask
// for a specific unit via Sc_unit = N+1. An empty string (the common
// case) lets the kernel pick the lowest free unit. A non-utun name is
// rejected rather than silently ignored, so a caller that passes "tun0"
// (the Linux default) gets a clear error instead of a surprising device
// name.
func openTUN(requestedName string, mtu uint32) (tunDevice, error) {
	unit, err := parseRequestedUnit(requestedName)
	if err != nil {
		return nil, err
	}

	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, cSYSPROTO_CONTROL)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_SYSTEM): %w (run with sudo)", err)
	}

	// Resolve the numeric control id for the utun control by name.
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], cUTUN_CONTROL_NAME)
	if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ioctl(CTLIOCGINFO) for %q: %w", cUTUN_CONTROL_NAME, err)
	}

	// Connecting the control socket is what actually creates the utun
	// interface. Sc_unit = N+1 requests utun<N>; 0 lets the kernel pick
	// the lowest free unit. SockaddrCtl.Unit maps to Sc_unit directly.
	if err := unix.Connect(fd, &unix.SockaddrCtl{ID: ctlInfo.Id, Unit: unit}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("connect utun (unit=%d): %w (run with sudo)", unit, err)
	}

	// Read the kernel-assigned interface name back.
	name, err := unix.GetsockoptString(fd, cSYSPROTO_CONTROL, cUTUN_OPT_IFNAME)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("getsockopt(UTUN_OPT_IFNAME): %w", err)
	}
	name = strings.TrimRight(name, "\x00")

	f := os.NewFile(uintptr(fd), "utun:"+name)

	if err := setIFMTU(name, mtu); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("set MTU on %s: %w", name, err)
	}
	if err := setIFUp(name); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("bring %s up: %w", name, err)
	}

	return &darwinTUN{
		f:    f,
		name: name,
		rbuf: make([]byte, int(mtu)+utunHeaderLen),
		wbuf: make([]byte, int(mtu)+utunHeaderLen),
	}, nil
}

// Read returns one whole IPv6 packet with the 4-byte utun family header
// stripped. A datagram shorter than the header (which the kernel never
// produces) is reported as a zero-length read so the caller's loop can
// ignore it rather than mis-parsing a runt packet.
func (t *darwinTUN) Read(p []byte) (int, error) {
	n, err := t.f.Read(t.rbuf)
	if err != nil {
		return 0, err
	}
	if n <= utunHeaderLen {
		return 0, nil
	}
	payload := n - utunHeaderLen
	if payload > len(p) {
		payload = len(p)
	}
	copy(p, t.rbuf[utunHeaderLen:utunHeaderLen+payload])
	return payload, nil
}

// Write prepends the protocol-family header utun requires and writes the
// combined frame in a single syscall. The tunnel is dual-stack, so the
// family is derived from the IP version nibble of the packet: 6 → AF_INET6,
// 4 → AF_INET. A packet too short to classify is dropped (reported as 0
// written) rather than mis-framed.
func (t *darwinTUN) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	prefix := afInet6Prefix
	switch p[0] >> 4 {
	case 6:
		prefix = afInet6Prefix
	case 4:
		prefix = afInetPrefix
	default:
		// Not an IPv4 or IPv6 packet — utun would reject it. Drop.
		return len(p), nil
	}
	frame := t.wbuf[:utunHeaderLen+len(p)]
	copy(frame[:utunHeaderLen], prefix[:])
	copy(frame[utunHeaderLen:], p)
	n, err := t.f.Write(frame)
	if err != nil {
		return 0, err
	}
	// Report the payload bytes written, not the framed length, so the
	// caller's accounting matches what it handed us.
	if n < utunHeaderLen {
		return 0, nil
	}
	return n - utunHeaderLen, nil
}

func (t *darwinTUN) Close() error { return t.f.Close() }
func (t *darwinTUN) Name() string { return t.name }

// parseRequestedUnit maps a requested device name to the Sc_unit value
// Connect wants. Empty → 0 (kernel picks). "utunN" → N+1 (utun units are
// 1-based in the control socket API; Sc_unit=1 yields utun0). Anything
// else is an error.
func parseRequestedUnit(name string) (uint32, error) {
	if name == "" {
		return 0, nil
	}
	if !strings.HasPrefix(name, "utun") {
		return 0, fmt.Errorf("netstack: macOS TUN device name must be of the form utunN (got %q); leave it empty to let the kernel pick", name)
	}
	numPart := strings.TrimPrefix(name, "utun")
	n, err := strconv.Atoi(numPart)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("netstack: invalid utun unit in %q", name)
	}
	return uint32(n + 1), nil
}

// ConfigureRoutes wires the per-session /48 to the utun device. Like the
// Linux implementation it needs root (the same privilege the utun open
// already required) and touches the host routing table, which the
// netstack package does not own.
//
// hostAddr is assigned to the interface so the kernel has a valid source
// address for traffic into the /48. The gVisor gateway address
// (<prefix>::1) is intentionally NOT assigned to the interface: if it
// were, the kernel would deliver packets destined for it locally and
// never write them into the utun fd, making the in-stack DNS listener
// unreachable.
//
// We shell out to `ifconfig` + `route` here. The equivalent via the
// PF_ROUTE socket is doable but adds a non-trivial helper for no benefit
// while we still require root anyway; we can revisit if/when we drop the
// sudo requirement (post-GA, per RD-207).
func ConfigureRoutes(cfg RouteConfig) error {
	if !commandExists("ifconfig") {
		return fmt.Errorf("netstack: `ifconfig` not found in PATH")
	}
	if !commandExists("route") {
		return fmt.Errorf("netstack: `route` not found in PATH")
	}

	// --- IPv6 ---
	// Assign the v6 host address to the utun interface. macOS uses
	// `ifconfig <dev> inet6 <addr> prefixlen 128` for a single-address
	// assignment. Re-running on an already-configured interface returns
	// "File exists"; tolerate it for idempotency.
	if err := runCmd("ifconfig", cfg.Device, "inet6", cfg.HostAddr, "prefixlen", "128"); err != nil {
		if !strings.Contains(err.Error(), "File exists") {
			return err
		}
	}
	if err := addRoute(cfg.Device, "-inet6", cfg.Prefix); err != nil {
		return err
	}

	// --- IPv4 ---
	// Assign the v4 host address. macOS point-to-point utun wants
	// `ifconfig <dev> inet <addr> <peer>`; using the same address for both
	// local and peer is the conventional way to put a /32-style host
	// address on a utun. The kernel then has a valid v4 source address for
	// traffic into the CGNAT range.
	if err := runCmd("ifconfig", cfg.Device, "inet", cfg.HostAddrV4, cfg.HostAddrV4); err != nil {
		if !strings.Contains(err.Error(), "File exists") {
			return err
		}
	}
	if err := addRoute(cfg.Device, "-inet", cfg.PrefixV4); err != nil {
		return err
	}
	return nil
}

// addRoute adds a route for prefix at the interface, replacing any stale
// entry (a "File exists" from a crashed previous run that left a route
// pointing at a now-dead interface). family is "-inet" or "-inet6".
func addRoute(device, family, prefix string) error {
	err := runCmd("route", "-n", "add", family, prefix, "-interface", device)
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "File exists") {
		return err
	}
	_ = runCmd("route", "-n", "delete", family, prefix, "-interface", device)
	return runCmd("route", "-n", "add", family, prefix, "-interface", device)
}

// UnconfigureRoutes is the inverse of ConfigureRoutes. Best-effort: the
// utun interface (and every route through it) disappears the moment the
// fd is closed, so a failure here is harmless — we run it anyway so a
// long-lived host doesn't accumulate stale routes if the kernel is slow
// to reap the interface.
func UnconfigureRoutes(cfg RouteConfig) {
	_ = runCmd("route", "-n", "delete", "-inet6", cfg.Prefix, "-interface", cfg.Device)
	_ = runCmd("ifconfig", cfg.Device, "inet6", cfg.HostAddr, "-alias")
	_ = runCmd("route", "-n", "delete", "-inet", cfg.PrefixV4, "-interface", cfg.Device)
	_ = runCmd("ifconfig", cfg.Device, "inet", cfg.HostAddrV4, "-alias")
}

// setIFMTU sets the interface MTU via ifconfig. macOS has no
// SIOCSIFMTU-via-AF_INET6-socket convention as clean as Linux's, and
// `ifconfig <dev> mtu <n>` is the documented path.
func setIFMTU(name string, mtu uint32) error {
	return runCmd("ifconfig", name, "mtu", strconv.FormatUint(uint64(mtu), 10))
}

// setIFUp brings the interface up. A freshly-created utun starts down;
// `ifconfig <dev> up` is the macOS equivalent of Linux's IFF_UP ioctl.
func setIFUp(name string) error {
	return runCmd("ifconfig", name, "up")
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
