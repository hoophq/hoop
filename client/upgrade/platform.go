package upgrade

import (
	"fmt"
	"runtime"
	"strings"
)

// ReleasesBaseURL is the base URL where signed release artifacts live.
// Layout under this base mirrors what `scripts/install-cli.sh` and
// `scripts/publish-release.sh` produce, e.g.
//
//	<base>/<version>/hoop_<version>_<OS>_<ARCH>.tar.gz
//	<base>/<version>/checksums.txt
const ReleasesBaseURL = "https://releases.hoop.dev/release"

// Platform identifies an OS/Arch pair using the labels the release pipeline
// embeds in artifact filenames (e.g. "Darwin_arm64", "Linux_x86_64").
type Platform struct {
	OS   string
	Arch string
}

// String returns the canonical "<OS>_<ARCH>" label.
func (p Platform) String() string {
	return p.OS + "_" + p.Arch
}

// CurrentPlatform returns the Platform for the running process.
// Returns an error if the OS/Arch pair has no matching release artifact.
func CurrentPlatform() (Platform, error) {
	return platformFor(runtime.GOOS, runtime.GOARCH)
}

// platformFor maps Go's GOOS/GOARCH constants to the release labels.
// Split from CurrentPlatform so unit tests can exercise the mapping.
func platformFor(goos, goarch string) (Platform, error) {
	var osLabel string
	switch goos {
	case "darwin":
		osLabel = "Darwin"
	case "linux":
		osLabel = "Linux"
	case "windows":
		osLabel = "Windows"
	default:
		return Platform{}, fmt.Errorf("unsupported OS %q: no hoop release artifact is published for this platform", goos)
	}

	var archLabel string
	switch goarch {
	case "amd64":
		archLabel = "x86_64"
	case "arm64":
		archLabel = "arm64"
	default:
		return Platform{}, fmt.Errorf("unsupported arch %q: no hoop release artifact is published for this architecture", goarch)
	}

	return Platform{OS: osLabel, Arch: archLabel}, nil
}

// ArtifactName returns the tarball filename for a version and platform,
// matching the format produced by the release pipeline.
func ArtifactName(version string, p Platform) string {
	return fmt.Sprintf("hoop_%s_%s.tar.gz", version, p)
}

// ExecutableName returns the hoop executable filename packaged inside the
// release artifact for this platform. Windows binaries carry a .exe
// suffix because the Go toolchain appends it for GOOS=windows builds, so
// the Windows tarball contains hoop.exe rather than hoop.
func (p Platform) ExecutableName() string {
	return executableName(p.OS == "Windows")
}

// executableName is the single source of truth for the OS-dependent hoop
// binary filename. It is shared by Platform.ExecutableName (which keys off
// the release OS label) and the host-side layout (which keys off
// runtime.GOOS) so the two can never drift.
func executableName(windows bool) string {
	if windows {
		return "hoop.exe"
	}
	return "hoop"
}

// ArtifactURL returns the full https URL for the release tarball.
func ArtifactURL(version string, p Platform) string {
	return fmt.Sprintf("%s/%s/%s", ReleasesBaseURL, version, ArtifactName(version, p))
}

// ChecksumsURL returns the URL of the checksums.txt for a given version.
func ChecksumsURL(version string) string {
	return fmt.Sprintf("%s/%s/checksums.txt", ReleasesBaseURL, version)
}

// NormalizeVersion strips an optional leading "v" so callers may write
// either "1.72.0" or "v1.72.0".
func NormalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}
