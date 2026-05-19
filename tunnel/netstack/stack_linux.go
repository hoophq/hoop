//go:build linux

package netstack

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux TUN ioctl numbers and flags. Defined in <linux/if_tun.h>.
const (
	cIFF_TUN         = 0x0001
	cIFF_NO_PI       = 0x1000
	cIFNAMSIZ        = 16
	cTUNSETIFF       = 0x400454ca
	cSIOCSIFMTU      = 0x8922
	cSIOCSIFFLAGS    = 0x8914
	cIFF_UP    int16 = 0x1
)

type ifreq struct {
	Name  [cIFNAMSIZ]byte
	Flags uint16
	_pad  [40 - cIFNAMSIZ - 2]byte
}

type ifreqMTU struct {
	Name [cIFNAMSIZ]byte
	MTU  int32
	_pad [40 - cIFNAMSIZ - 4]byte
}

type ifreqFlags struct {
	Name  [cIFNAMSIZ]byte
	Flags int16
	_pad  [40 - cIFNAMSIZ - 2]byte
}

type linuxTUN struct {
	f    *os.File
	name string
}

// openTUN opens /dev/net/tun, registers a new TUN device, brings it up, and
// returns a tunDevice. requestedName may be empty (kernel picks).
func openTUN(requestedName string, mtu uint32) (tunDevice, error) {
	if len(requestedName) >= cIFNAMSIZ {
		return nil, fmt.Errorf("netstack: TUN name %q too long (max %d)", requestedName, cIFNAMSIZ-1)
	}

	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w (run with sudo or set cap_net_admin+ep on the binary)", err)
	}

	var req ifreq
	copy(req.Name[:], requestedName)
	req.Flags = cIFF_TUN | cIFF_NO_PI
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(cTUNSETIFF), uintptr(unsafe.Pointer(&req))); errno != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}
	name := cString(req.Name[:])

	// Set non-blocking on the fd so reads return EAGAIN we can ignore;
	// actually we use the *os.File blocking model below — leave default.
	f := os.NewFile(uintptr(fd), "tun:"+name)

	if err := setIFMTU(name, int32(mtu)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("set MTU: %w", err)
	}
	if err := setIFUp(name); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("set up: %w", err)
	}

	return &linuxTUN{f: f, name: name}, nil
}

func (t *linuxTUN) Read(p []byte) (int, error)  { return t.f.Read(p) }
func (t *linuxTUN) Write(p []byte) (int, error) { return t.f.Write(p) }
func (t *linuxTUN) Close() error                { return t.f.Close() }
func (t *linuxTUN) Name() string                { return t.name }

func cString(b []byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// setIFMTU calls SIOCSIFMTU via an inet socket (the documented way to
// configure a netdev's MTU from userspace).
func setIFMTU(name string, mtu int32) error {
	sock, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sock)
	var req ifreqMTU
	copy(req.Name[:], name)
	req.MTU = mtu
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), uintptr(cSIOCSIFMTU), uintptr(unsafe.Pointer(&req))); errno != 0 {
		return errno
	}
	return nil
}

func setIFUp(name string) error {
	sock, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sock)
	var req ifreqFlags
	copy(req.Name[:], name)
	req.Flags = cIFF_UP
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(sock), uintptr(cSIOCSIFFLAGS), uintptr(unsafe.Pointer(&req))); errno != 0 {
		return errno
	}
	return nil
}

// ConfigureRoutes wires the per-session /48 to the TUN device and points the
// resolver address at port 53. It must be called by the binary after New
// because it needs CAP_NET_ADMIN — same privilege the TUN open already
// required — and it touches the host's routing table which the netstack
// package does not own.
//
// We shell out to `ip` here. Doing the equivalent via rtnetlink is doable
// but adds a chunky helper for no spike-time benefit; we'll revisit when we
// drop the sudo requirement.
func ConfigureRoutes(deviceName string, prefix string, gateway string) error {
	if !commandExists("ip") {
		return fmt.Errorf("netstack: `ip` not found in PATH; install iproute2")
	}
	// Add the gateway address to the link so the kernel knows the local IP.
	if err := runIP("addr", "add", gateway+"/128", "dev", deviceName); err != nil {
		// "RTNETLINK answers: File exists" on re-runs is fine.
		if !strings.Contains(err.Error(), "File exists") {
			return err
		}
	}
	if err := runIP("-6", "route", "replace", prefix, "dev", deviceName); err != nil {
		return err
	}
	return nil
}

// UnconfigureRoutes is the inverse of ConfigureRoutes. Best-effort.
func UnconfigureRoutes(deviceName string, prefix string, gateway string) {
	_ = runIP("-6", "route", "del", prefix, "dev", deviceName)
	_ = runIP("addr", "del", gateway+"/128", "dev", deviceName)
}

func runIP(args ...string) error {
	cmd := exec.Command("ip", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

