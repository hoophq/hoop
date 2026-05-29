//go:build darwin

package resolved

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// macOS DNS suffix routing (RD-208) does not use systemd-resolved — it
// uses the BSD/mDNSResponder per-domain resolver mechanism documented in
// resolver(5): a file at /etc/resolver/<domain> whose contents name the
// DNS server to use for *.<domain> queries. mDNSResponder watches that
// directory and picks up changes automatically; no daemon reload, no CLI
// call.
//
// A file like:
//
//	# /etc/resolver/hoop
//	nameserver fd3f:61df:3c04::1
//	port 53
//
// tells the resolver that every query for a name ending in `.hoop` goes
// to fd3f:61df:3c04::1:53. Every other query keeps using the system's
// normal DNS path, exactly like the `~hoop` routing-only domain we set on
// Linux.
//
// Why this maps onto the Linux-shaped Configurer interface
//
//   - Configure writes the file. It is keyed on Config.SearchDomain
//     (the *.hoop suffix), NOT on Config.Device: mDNSResponder routes by
//     domain name, and the utun interface name is irrelevant to it. We
//     still validate Device for parity with the Linux contract so a
//     caller that forgets to set it gets the same error on both
//     platforms.
//
//   - Unconfigure removes the file. The interface contract passes only
//     the device name, which macOS cannot map back to a resolver file —
//     so the darwin configurer is stateful: Configure records the
//     written domain and Unconfigure removes that file. This keeps the
//     cross-platform interface unchanged while remaining correct.
//
// Detection
//
// /etc/resolver is the well-known location resolver(5) documents and
// mDNSResponder always watches; it may not exist on a pristine system,
// so Configure creates it (mode 0755, the standard for that directory).
// Unlike Linux there is no "is the DNS manager running" probe — on macOS
// mDNSResponder is always PID-managed by launchd and always present, so
// the darwin path never returns ErrUnsupported. (ErrUnsupported stays in
// the package for the Linux no-resolved case and the non-Linux/non-darwin
// stub.)
//
// Self-healing after an ungraceful exit
//
// RD-208 requires that a stale /etc/resolver/<domain> from a crashed
// previous run be cleaned up. Configure is inherently self-healing: it
// truncates and rewrites the file every bring-up, so a stale file from a
// previous run is overwritten with current contents (same domain → same
// resolver IP if the session seed is unchanged; a different IP simply
// replaces the dead one). For the "daemon starts but never brings the
// tunnel up" case, the package also exposes CleanupStale (called from the
// daemon at startup) to unconditionally remove any resolver file we may
// have left behind.

// resolverDir is the directory mDNSResponder watches for per-domain
// resolver files. A var (not const) so unit tests can redirect it to a
// t.TempDir().
var resolverDir = "/etc/resolver"

// newPlatformConfigurer returns the macOS /etc/resolver-backed
// Configurer. Never nil.
func newPlatformConfigurer() Configurer { return &darwinConfigurer{} }

// darwinConfigurer drives mDNSResponder via /etc/resolver files. It is
// stateful: it remembers the domain it last configured so Unconfigure
// (which only receives a device name in the cross-platform interface)
// can find and remove the right file.
type darwinConfigurer struct {
	mu     sync.Mutex
	domain string // the SearchDomain last passed to Configure; "" when idle
}

// Configure writes /etc/resolver/<SearchDomain> pointing mDNSResponder at
// the tunnel's in-stack resolver. Idempotent: re-running with the same
// Config rewrites identical contents.
func (d *darwinConfigurer) Configure(cfg Config) error {
	if cfg.Device == "" {
		return errors.New("resolved: Configure called with empty Device")
	}
	if cfg.DNSAddress == "" {
		return errors.New("resolved: Configure called with empty DNSAddress")
	}
	if cfg.SearchDomain == "" {
		return errors.New("resolved: Configure called with empty SearchDomain")
	}

	if err := os.MkdirAll(resolverDir, 0755); err != nil {
		return fmt.Errorf("resolved: mkdir %s: %w", resolverDir, err)
	}

	path := resolverFilePath(cfg.SearchDomain)
	body := fmt.Sprintf("# Managed by hsh-tunneld — do not edit.\n# Routes *.%s queries to the tunnel resolver.\nnameserver %s\nport 53\n",
		cfg.SearchDomain, cfg.DNSAddress)

	// Write atomically (temp + rename) so mDNSResponder never observes a
	// half-written file. The directory is root-owned; the daemon runs as
	// root via the LaunchDaemon, so 0644 contents are fine — the file is
	// not secret.
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		return fmt.Errorf("resolved: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("resolved: rename %s -> %s: %w", tmp, path, err)
	}

	d.mu.Lock()
	d.domain = cfg.SearchDomain
	d.mu.Unlock()
	return nil
}

// Unconfigure removes the resolver file written by the most recent
// Configure. The device argument is part of the cross-platform contract
// but unused on macOS (mDNSResponder keys on the domain, not the
// interface). Best-effort: a missing file is success.
func (d *darwinConfigurer) Unconfigure(device string) error {
	if device == "" {
		return errors.New("resolved: Unconfigure called with empty device")
	}
	d.mu.Lock()
	domain := d.domain
	d.domain = ""
	d.mu.Unlock()

	if domain == "" {
		// Configure never succeeded (or already torn down). Nothing to do.
		return nil
	}
	if err := os.Remove(resolverFilePath(domain)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("resolved: remove %s: %w", resolverFilePath(domain), err)
	}
	return nil
}

// CleanupStale unconditionally removes the /etc/resolver/<domain> file
// for the given domain, regardless of whether this process wrote it. The
// daemon calls it at startup (before any bring-up) so a resolver file
// orphaned by a SIGKILL/crash of a previous run is cleared, satisfying
// RD-208's "each platform startup must self-heal stale rules" rule.
//
// Best-effort: a missing file (the common case) is success. It is a
// package-level function rather than a Configurer method because it runs
// before any tunnel exists, when there is no live Configurer state to
// consult.
func CleanupStale(domain string) error {
	if domain == "" {
		return errors.New("resolved: CleanupStale called with empty domain")
	}
	if err := os.Remove(resolverFilePath(domain)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("resolved: cleanup stale %s: %w", resolverFilePath(domain), err)
	}
	return nil
}

// resolverFilePath returns the /etc/resolver/<domain> path for a domain.
func resolverFilePath(domain string) string {
	return filepath.Join(resolverDir, domain)
}
