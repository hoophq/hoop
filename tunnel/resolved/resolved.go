// Package resolved teaches systemd-resolved about the tunnel's
// per-interface DNS server, so a host using the resolved stub
// (127.0.0.53) can resolve *.hoop names via the gVisor resolver
// without the user having to run `resolvectl` manually.
//
// # Why a package, not an inline helper
//
// The lifecycle is intentionally a peer of `netstack.ConfigureRoutes`
// + `UnconfigureRoutes`: the tunnel manager calls Configure on
// bring-up after routes are in place, and Unconfigure on tear-down
// before routes go away. Keeping that contract narrow + symmetric
// is what lets RD-209 (live reconnect) and any future "wipe DNS on
// crash" recovery code reuse the same primitives.
//
// We deliberately do NOT use D-Bus to talk to org.freedesktop.resolve1
// directly. The dbus call surface is slightly richer than what
// `resolvectl` exposes, but:
//
//   - resolvectl is shipped in the systemd binary package on every
//     distro that uses systemd-resolved; whereas the godbus dep
//     would pull a non-trivial dependency tree into our binary.
//
//   - The resolvectl CLI's behavior is the documented stable contract.
//     The dbus interface is technically also stable but only the CLI
//     gets the systemd-side compatibility shims when interfaces
//     change between versions.
//
//   - Shelling out is observable in `journalctl -u hsh-tunneld` and
//     in `ps` for operators debugging weird DNS state. dbus traffic
//     is invisible without dbus-monitor.
//
// The cost of shelling out is one fork+exec per bring-up + one per
// tear-down, both off the hot path.
//
// # Detection
//
// We declare systemd-resolved "present and managing DNS" when both:
//
//   - /run/systemd/resolve/ exists. This directory is created by the
//     systemd-resolved unit on activation and removed on stop. Its
//     presence is the canonical "the daemon is running" signal
//     (more reliable than checking the dbus name, which is async).
//
//   - resolvectl is on PATH. Without it we can't drive the daemon
//     even if it's running, so we fall back to the manual-hint
//     banner.
//
// We deliberately do NOT check /etc/resolv.conf's symlink target.
// Stock distros symlink it to /run/systemd/resolve/stub-resolv.conf;
// users who run their own resolver (dnscrypt-proxy, unbound) may
// override the symlink without disabling systemd-resolved. In that
// case our resolved-registration succeeds but has no visible effect,
// which is fine — the banner-hint fallback still tells them how to
// wire DNS through their own resolver.
//
// # Failure handling
//
// Configure returns ErrUnsupported when the host doesn't run
// systemd-resolved; the caller logs + prints the manual hint as
// before. Any other resolvectl error is wrapped and returned as-is;
// the caller falls back to the manual hint the same way and the
// tunnel still comes up. Bringing the tunnel up MUST NOT be blocked
// by a flaky resolvectl call.
//
// Unconfigure is best-effort and returns nil on any error — we're
// usually tearing down because the operator wants the daemon gone,
// and a noisy "couldn't unwire DNS" error at that point is worse
// than the harmless dangling per-link config (which goes away
// anyway when the interface itself disappears).
package resolved

import (
	"errors"
)

// ErrUnsupported indicates this host does not run systemd-resolved
// (or resolvectl is not installed). The tunnel manager treats this
// as a "fall through to the manual hint" signal, not as a hard
// failure.
var ErrUnsupported = errors.New("resolved: systemd-resolved is not the active DNS manager on this host")

// Config is the per-bring-up configuration of the resolved-side
// DNS routing. Everything is plain strings rather than `net.IP` /
// nominal types because the resolvectl CLI takes strings anyway,
// and the daemon already has all of these as strings (from the
// tunnel snapshot).
type Config struct {
	// Device is the TUN interface name (e.g. "tun0"). resolvectl
	// keys all its per-link state on this name; an empty value here
	// is a programming error and Configure rejects it.
	Device string

	// DNSAddress is the IP address of the gVisor stub resolver
	// inside the tunnel (e.g. "fd3f:61df:3c04::1"). Pass as-is to
	// `resolvectl dns <iface> <addr>`. Must be a literal IP, not
	// a hostname — resolvectl does not resolve.
	DNSAddress string

	// SearchDomain is the routing-only domain the tunnel owns
	// (typically "hoop"). resolvectl receives it prefixed with `~`
	// (the routing-only marker) so resolved only forwards queries
	// for names ending in this suffix to our resolver — every other
	// query keeps going to the user's existing DNS path.
	SearchDomain string
}

// Configurer abstracts the platform-specific resolved-CLI driver.
// Linux gets a real implementation; every other platform gets a
// stub that returns ErrUnsupported.
//
// Why an interface rather than free functions: it makes testing
// the tunnel manager's bring-up logic easy without spinning up an
// actual resolvectl process. Tests pass a fake Configurer that
// records the calls; production wires in the linux impl.
type Configurer interface {
	// Configure registers the tunnel's DNS server + routing domain
	// with systemd-resolved for the given interface. Idempotent:
	// re-running with the same Config is cheap and changes nothing.
	//
	// Returns ErrUnsupported if systemd-resolved isn't running on
	// this host. Returns any other error from the resolvectl
	// invocation verbatim so callers can log a useful diagnostic.
	Configure(cfg Config) error

	// Unconfigure reverts whatever Configure set up for the given
	// interface. Equivalent to `resolvectl revert <iface>`.
	// Best-effort: any error is returned to the caller but the
	// tunnel teardown does NOT abort on it.
	Unconfigure(device string) error
}

// New returns the platform Configurer. Never nil — on unsupported
// platforms (every non-Linux GOOS) it returns a stub whose Configure
// returns ErrUnsupported.
func New() Configurer {
	return newPlatformConfigurer()
}
