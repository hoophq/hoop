//go:build !linux && !darwin

package resolved

// newPlatformConfigurer returns a stub that reports ErrUnsupported
// on every call. systemd-resolved is a Linux-only thing; macOS has
// its own implementation in resolved_darwin.go (per-domain
// /etc/resolver files); on Windows it's the Win32 DNS API / NRPT
// rules (RD-208 Windows follow-up).
//
// We don't return nil here — the tunnel manager would still call
// Configure on bring-up and we want a clean ErrUnsupported rather
// than a nil-pointer panic.
func newPlatformConfigurer() Configurer { return &unsupportedConfigurer{} }

type unsupportedConfigurer struct{}

func (unsupportedConfigurer) Configure(Config) error   { return ErrUnsupported }
func (unsupportedConfigurer) Unconfigure(string) error { return nil }

// CleanupStale is a no-op on platforms without persistent, file-based
// DNS routing state to self-heal. See the darwin implementation for the
// /etc/resolver case and resolved_linux.go for the rationale on Linux
// (per-interface state vanishes with the link).
func CleanupStale(domain string) error { return nil }
