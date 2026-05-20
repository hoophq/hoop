// Package upgrade implements the version-manager subsystem used by
// `hoop upgrade` and `hoop versions ...`. It manages CLI binaries under
// $HOME/.hoop/versions/<version>/hoop and keeps a single symlink at
// $HOME/.hoop/bin/hoop pointing at the active version.
//
// Inspired by nvm and fnm, but simplified: a single global active version
// is selected via one stable symlink, so users only need to add
// $HOME/.hoop/bin to their PATH once. No shell function is required.
package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
)

// File and directory names under $HOME/.hoop used by the version manager.
const (
	homeDirName       = ".hoop"
	binDirName        = "bin"
	versionsDirName   = "versions"
	binaryName        = "hoop"
	versionsStoreFile = "versions.toml"
)

// Layout resolves the absolute paths the version manager works with.
// All fields are absolute paths. Layout does not create any directories;
// callers must use EnsureDirs when they actually need to write.
type Layout struct {
	// Home is the hoop home directory ($HOME/.hoop).
	Home string
	// BinDir is the directory containing the active symlink ($HOME/.hoop/bin).
	BinDir string
	// BinLink is the symlink path users put on their PATH
	// ($HOME/.hoop/bin/hoop).
	BinLink string
	// VersionsDir is the parent directory for installed versions
	// ($HOME/.hoop/versions).
	VersionsDir string
	// StoreFile is the TOML file tracking installed versions and the
	// currently active version ($HOME/.hoop/versions.toml).
	StoreFile string
}

// DefaultLayout returns the layout rooted at the current user's home dir.
func DefaultLayout() (Layout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("failed obtaining home dir: %w", err)
	}
	return LayoutFromHome(home), nil
}

// LayoutFromHome returns a layout rooted at the provided HOME directory.
// Useful for tests.
func LayoutFromHome(home string) Layout {
	hoopHome := filepath.Join(home, homeDirName)
	return Layout{
		Home:        hoopHome,
		BinDir:      filepath.Join(hoopHome, binDirName),
		BinLink:     filepath.Join(hoopHome, binDirName, binaryName),
		VersionsDir: filepath.Join(hoopHome, versionsDirName),
		StoreFile:   filepath.Join(hoopHome, versionsStoreFile),
	}
}

// VersionDir returns the install directory for a specific version, e.g.
// $HOME/.hoop/versions/1.73.0.
func (l Layout) VersionDir(version string) string {
	return filepath.Join(l.VersionsDir, version)
}

// VersionBinary returns the path to the hoop binary for a specific version,
// e.g. $HOME/.hoop/versions/1.73.0/hoop.
func (l Layout) VersionBinary(version string) string {
	return filepath.Join(l.VersionDir(version), binaryName)
}

// EnsureDirs creates the bin and versions directories with 0700 perms.
// Safe to call repeatedly.
func (l Layout) EnsureDirs() error {
	for _, dir := range []string{l.Home, l.BinDir, l.VersionsDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed creating %s: %w", dir, err)
		}
	}
	return nil
}
