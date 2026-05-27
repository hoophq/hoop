package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrBinLinkConflict is returned when $HOME/.hoop/bin/hoop already
// exists but is a regular file or a symlink pointing outside of
// $HOME/.hoop/versions. We refuse to overwrite it because it may
// belong to the user — most often a stale `make build-dev-client`
// output from before the dev binary moved to $HOME/.hoop/dev/hoop.
var ErrBinLinkConflict = errors.New("hoop bin path is owned by something else; refusing to overwrite")

// SetActive atomically updates the active symlink at $HOME/.hoop/bin/hoop
// to point at the installed version's binary and updates the store's
// Active field. It does NOT save the store; callers do that explicitly.
//
// Returns ErrBinLinkConflict if a non-symlink file exists at the bin path
// or if an existing symlink resolves outside the versions directory.
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
	if runtime.GOOS == "windows" {
		return fmt.Errorf("setting the active version requires symlink privileges that are not available by default on Windows; install the matching version with brew/chocolatey or download from %s", ReleasesBaseURL)
	}

	if err := safeReplaceSymlink(l, target); err != nil {
		return err
	}
	store.Active = version
	return nil
}

// safeReplaceSymlink replaces the bin symlink with a new target pointing
// at the installed binary. It does so atomically using a sibling temp
// symlink and rename, mirroring how nvm/fnm do it.
func safeReplaceSymlink(l Layout, target string) error {
	binLink := l.BinLink
	if err := assertOwnedSymlink(l, binLink); err != nil {
		return err
	}

	tmp := binLink + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("failed creating temp symlink %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, binLink); err != nil {
		// best-effort cleanup
		os.Remove(tmp)
		return fmt.Errorf("failed updating symlink %s: %w", binLink, err)
	}
	return nil
}

// assertOwnedSymlink returns nil iff binLink doesn't exist, or exists and
// is a symlink that points inside $HOME/.hoop/versions/. Otherwise returns
// ErrBinLinkConflict.
//
// Both sides of the prefix comparison are resolved through
// filepath.EvalSymlinks so that platforms with symlinked tmpdirs
// (e.g. macOS /var -> /private/var) compare paths in the same namespace.
func assertOwnedSymlink(l Layout, binLink string) error {
	info, err := os.Lstat(binLink)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed stat %s: %w", binLink, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf(`%w:

%s exists as a regular file, not the symlink the version manager creates.

The most common cause is an older `+"`make build-dev-client`"+` run that
wrote the dev binary at this exact path. The dev binary now lives at
$HOME/.hoop/dev/hoop, and $HOME/.hoop/bin/hoop is reserved as the
symlink updated by `+"`hoop versions sync` / `hoop versions upgrade`"+`.

To recover, do one of the following and re-run the same hoop versions
command:
  - keep the dev build: mv "%s" "$HOME/.hoop/dev/hoop"
  - discard it:         rm "%s"`,
			ErrBinLinkConflict, binLink, binLink, binLink)
	}
	cur, err := os.Readlink(binLink)
	if err != nil {
		return fmt.Errorf("failed reading symlink %s: %w", binLink, err)
	}
	if !filepath.IsAbs(cur) {
		cur = filepath.Join(filepath.Dir(binLink), cur)
	}
	expectedPrefix := canonicalDir(l.VersionsDir)

	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Dangling symlink. Compare its literal target against the
			// expected prefix.
			if strings.HasPrefix(filepath.Clean(cur)+string(filepath.Separator), expectedPrefix) {
				return nil
			}
			return fmt.Errorf("%w: %s -> %s (dangling, outside %s)", ErrBinLinkConflict, binLink, cur, l.VersionsDir)
		}
		return fmt.Errorf("failed resolving symlink %s: %w", binLink, err)
	}
	cleaned := filepath.Clean(resolved) + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, expectedPrefix) {
		return fmt.Errorf("%w: %s -> %s (outside %s)", ErrBinLinkConflict, binLink, resolved, l.VersionsDir)
	}
	return nil
}

// canonicalDir returns a directory path with symlinks resolved and a
// trailing separator, suitable for HasPrefix comparisons. If the path
// can't be resolved (e.g. it doesn't exist yet) it falls back to a
// cleaned literal form.
func canonicalDir(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved) + string(filepath.Separator)
	}
	return filepath.Clean(p) + string(filepath.Separator)
}
