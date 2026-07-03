package upgrade

import (
	"errors"
	"fmt"
	"os"
)

// ErrBinLinkConflict is returned when the active-version path
// ($HOME/.hoop/bin/hoop, or hoop.exe on Windows) already exists but was
// not created by the version manager. On Unix that means it is a regular
// file or a symlink pointing outside $HOME/.hoop/versions; on Windows it
// means its contents don't match any installed version's binary. In both
// cases we refuse to overwrite it because it may belong to the user —
// most often a stale `make build-dev-client` output from before the dev
// binary moved to $HOME/.hoop/dev/hoop.
var ErrBinLinkConflict = errors.New("hoop bin path is owned by something else; refusing to overwrite")

// SetActive makes version the active one by pointing the bin path
// ($HOME/.hoop/bin/hoop) at the installed binary, then updates the
// store's Active field. It does NOT save the store; callers do that
// explicitly so install/activate semantics stay separable.
//
// The activation mechanism is OS-specific (see the activate function):
// a symlink on Unix, a copy on Windows where unprivileged symlinks are
// not available by default. Returns ErrBinLinkConflict when the bin path
// is owned by something other than the version manager.
func SetActive(l Layout, store *Store, version string) error {
	version = NormalizeVersion(version)
	target := l.VersionBinary(version)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("version %s is not installed (missing %s)", version, target)
	}
	if !store.Has(version) {
		return fmt.Errorf("version %s is not in the versions store; reinstall it with `hoop versions install %s`", version, version)
	}
	if err := l.EnsureDirs(); err != nil {
		return err
	}
	if err := activate(l, store, target); err != nil {
		return err
	}
	store.Active = version
	return nil
}
