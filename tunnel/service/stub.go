package service

import "fmt"

// stubManager is the placeholder implementation used by every platform
// that does not (yet) have a real service-manager backend. It always
// returns ErrUnsupportedPlatform with a wrapped, human-readable
// platform identifier so users running `sudo hsh-tunneld install` on
// macOS or Windows today see exactly which platform needs work
// rather than a generic failure.
//
// We deliberately don't make stubManager method-by-method "stub the
// mutating ones, allow Status" because Status with no real backend
// would have to return either StatusNotInstalled (lie) or an error
// (which is what ErrUnsupportedPlatform already conveys). One-shape
// stub is simpler.
type stubManager struct {
	// platform is the human-readable identifier returned from
	// PlatformName(). Lets the error from Install / Uninstall etc. say
	// "service: not yet supported on launchd" rather than the goos
	// string.
	platform string
}

func (s *stubManager) PlatformName() string { return s.platform }

func (s *stubManager) IsElevated() bool {
	// Best-effort: defer to runtime.GOOS-specific helpers later. We
	// return false so the elevation check in the CLI verb prints a
	// "re-run as admin" message that is at least directionally
	// correct on every platform.
	return false
}

func (s *stubManager) Install(Options) error {
	return fmt.Errorf("%w (%s)", ErrUnsupportedPlatform, s.platform)
}

func (s *stubManager) Uninstall(PurgeOptions) error {
	return fmt.Errorf("%w (%s)", ErrUnsupportedPlatform, s.platform)
}

func (s *stubManager) Status() (Status, error) {
	return StatusNotInstalled, fmt.Errorf("%w (%s)", ErrUnsupportedPlatform, s.platform)
}

func (s *stubManager) Start() error {
	return fmt.Errorf("%w (%s)", ErrUnsupportedPlatform, s.platform)
}

func (s *stubManager) Stop() error {
	return fmt.Errorf("%w (%s)", ErrUnsupportedPlatform, s.platform)
}
