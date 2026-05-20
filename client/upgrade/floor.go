package upgrade

import (
	"errors"
	"fmt"

	"golang.org/x/mod/semver"
)

// MinInstallableMinor is the lowest MAJOR.MINOR a hoop release must
// carry for the version manager to manage it. Versions below this floor
// don't ship the `hoop upgrade` / `hoop versions` commands, so installing
// them would orphan the user on a CLI that can't manage itself.
//
// The prefix "v" matches what golang.org/x/mod/semver expects. Update
// this constant if the version manager is back-ported to an older minor.
const MinInstallableMinor = "v1.74"

// Sentinel errors returned by ValidateInstallableVersion. Callers (the
// `hoop upgrade` command in particular) use errors.Is to detect the
// specific case and render a tailored message.
var (
	// ErrUnknownGatewayVersion means the version string is empty or the
	// literal "unknown" — the marker [common/version.Get] emits for a
	// build that wasn't stamped with a release tag. Almost always a local
	// dev gateway; upgrading the CLI to match doesn't make sense.
	ErrUnknownGatewayVersion = errors.New("gateway did not report a release version")

	// ErrInvalidVersion means the version string isn't a parseable semver
	// at all (e.g. "banana", "1.2"). Usually indicates a misconfigured
	// gateway or a non-hoop endpoint behind the api_url.
	ErrInvalidVersion = errors.New("not a valid semantic version")

	// ErrBelowFloor means the version is a real semver but predates the
	// release line in which the version manager shipped.
	ErrBelowFloor = errors.New("version predates the hoop version manager")
)

// ValidateInstallableVersion returns an error if target cannot be
// installed by the version manager. The returned error wraps one of the
// Err* sentinels above (with %w) so callers can branch on errors.Is.
//
// The target should NOT carry a leading "v" — callers normalize through
// NormalizeVersion first.
func ValidateInstallableVersion(target string) error {
	if target == "" || target == "unknown" {
		return fmt.Errorf("%w (got %q)", ErrUnknownGatewayVersion, target)
	}
	v := "v" + target
	if !semver.IsValid(v) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, target)
	}
	if semver.Compare(semver.MajorMinor(v), MinInstallableMinor) < 0 {
		return fmt.Errorf("%w: %s is older than %s.0", ErrBelowFloor, target, MinInstallableMinor[1:])
	}
	return nil
}
