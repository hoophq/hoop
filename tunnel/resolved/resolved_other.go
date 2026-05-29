//go:build !linux

package resolved

// newPlatformConfigurer returns a stub that reports ErrUnsupported
// on every call. systemd-resolved is a Linux-only thing; on Darwin
// the equivalent is `scutil --dns`/scoped resolvers (RD-217 macOS
// follow-up) and on Windows it's the Win32 DNS API
// (RD-217 Windows follow-up).
//
// We don't return nil here — the tunnel manager would still call
// Configure on bring-up and we want a clean ErrUnsupported rather
// than a nil-pointer panic.
func newPlatformConfigurer() Configurer { return &unsupportedConfigurer{} }

type unsupportedConfigurer struct{}

func (unsupportedConfigurer) Configure(Config) error      { return ErrUnsupported }
func (unsupportedConfigurer) Unconfigure(string) error    { return nil }
