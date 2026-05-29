//go:build linux

package resolved

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runtimeDir is systemd-resolved's per-host activation marker. The
// systemd unit creates it on start and removes it on stop, so
// presence here is the canonical "the daemon is running" signal.
// Made a var (not const) so unit tests can point it at a t.TempDir().
var runtimeDir = "/run/systemd/resolve"

// resolvectlOverride lets unit tests bypass exec.LookPath and force
// the package to use a specific binary (typically a shell script
// recording its arguments). Empty in production; the real lookup
// runs via exec.LookPath on every Configure/Unconfigure call.
//
// We deliberately do not cache the LookPath result in a package
// variable: a real PATH change between calls is rare but legal
// (test environments toggling it; a host where systemd-resolved
// was installed mid-session), and the lookup itself is cheap.
var resolvectlOverride = ""

// newPlatformConfigurer returns the Linux/systemd-resolved-backed
// Configurer. Never nil. Detection runs lazily at Configure time
// so a host that gains systemd-resolved between daemon-start and
// first bring-up still picks it up.
func newPlatformConfigurer() Configurer { return &linuxConfigurer{} }

// linuxConfigurer drives systemd-resolved via the resolvectl CLI.
// Stateless — all per-bring-up state lives in resolved itself
// (keyed on the interface name).
type linuxConfigurer struct{}

// Configure issues two resolvectl invocations:
//
//	resolvectl dns    <iface> <addr>     - tells resolved the upstream
//	resolvectl domain <iface> ~<domain>  - marks this iface as the
//	                                       authoritative resolver
//	                                       for *.<domain>
//
// Both are idempotent: re-running with the same args is a no-op.
// We deliberately do NOT use the combined `resolvectl interface
// <iface> dns ...` form: older resolvectl versions (Ubuntu 20.04)
// only support the per-property syntax above.
func (l *linuxConfigurer) Configure(cfg Config) error {
	if cfg.Device == "" {
		return errors.New("resolved: Configure called with empty Device")
	}
	if cfg.DNSAddress == "" {
		return errors.New("resolved: Configure called with empty DNSAddress")
	}
	if cfg.SearchDomain == "" {
		return errors.New("resolved: Configure called with empty SearchDomain")
	}

	if !detect() {
		return ErrUnsupported
	}

	bin, err := resolveBinary()
	if err != nil {
		return err
	}

	// dns first, then domain. If `dns` fails we don't want to leave
	// a stale routing-domain entry pointing at a server that isn't
	// registered yet — bail before the second call.
	if err := run(bin, "dns", cfg.Device, cfg.DNSAddress); err != nil {
		return fmt.Errorf("resolvectl dns %s %s: %w", cfg.Device, cfg.DNSAddress, err)
	}

	// The leading "~" turns this into a routing-only domain: queries
	// for `*.<SearchDomain>` are forwarded to the dns server we
	// registered above, but resolved.conf-driven default behavior
	// for every other name is preserved. Without the "~", resolved
	// would treat the iface as the search-domain provider for the
	// host and break unrelated DNS lookups.
	if err := run(bin, "domain", cfg.Device, "~"+cfg.SearchDomain); err != nil {
		return fmt.Errorf("resolvectl domain %s ~%s: %w", cfg.Device, cfg.SearchDomain, err)
	}
	return nil
}

// Unconfigure runs `resolvectl revert <iface>`. resolvectl handles
// the "interface doesn't exist anymore" case gracefully (the link
// has gone away when the TUN fd was closed by the netstack package
// shutdown), so this rarely produces useful output.
func (l *linuxConfigurer) Unconfigure(device string) error {
	if device == "" {
		return errors.New("resolved: Unconfigure called with empty device")
	}
	if !detect() {
		return nil // host doesn't run resolved; nothing to revert
	}
	bin, err := resolveBinary()
	if err != nil {
		return err
	}
	// `revert` undoes every per-link setting (dns, domain, etc).
	// Don't return non-nil on "Link does not exist" — that's the
	// common case when called *after* netstack.UnconfigureRoutes
	// has destroyed the TUN device.
	if err := run(bin, "revert", device); err != nil {
		if strings.Contains(err.Error(), "Link") && strings.Contains(err.Error(), "does not") {
			return nil
		}
		return fmt.Errorf("resolvectl revert %s: %w", device, err)
	}
	return nil
}

// detect reports whether systemd-resolved is the active DNS manager
// on this host. Cheap: a single stat. Re-evaluated on every call
// so a Configure invocation that races a systemd-resolved restart
// gets a fresh answer.
func detect() bool {
	info, err := os.Stat(runtimeDir)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

// resolveBinary returns the path to resolvectl. Honours
// resolvectlOverride (tests) then falls back to PATH lookup.
func resolveBinary() (string, error) {
	if resolvectlOverride != "" {
		return resolvectlOverride, nil
	}
	bin, err := exec.LookPath("resolvectl")
	if err != nil {
		return "", fmt.Errorf("%w: resolvectl not found on PATH", ErrUnsupported)
	}
	return bin, nil
}

// run executes a resolvectl invocation and folds stdout/stderr into
// the error on non-zero exit. The output is helpful in the journal
// when resolvectl complains about e.g. an interface name typo;
// keeping it inside the wrapped error means callers don't need to
// build their own diagnostic strings.
func run(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
