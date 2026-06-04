package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// copyActivate makes target the active binary by copying it to the bin
// path. It is the Windows activation strategy: unprivileged symlinks are
// not available there by default, so a copy is the robust, dependency-free
// alternative.
//
// Windows will not let a process overwrite or delete a running executable,
// but it WILL let one be renamed. So when the bin copy is the very process
// performing the upgrade, copyActivate first renames the live binary aside
// (hoop.exe.old-<nanos>) and then drops the new binary into place. Retired
// copies are swept on a later run, once the process holding them exits.
//
// The function refuses to clobber a bin file the manager didn't create:
// see assertOwnedCopy.
func copyActivate(l Layout, store *Store, target string) error {
	binPath := l.BinLink
	if err := assertOwnedCopy(l, store, binPath); err != nil {
		return err
	}
	dir := filepath.Dir(binPath)

	// Stage the new binary next to the destination so the final move is a
	// same-directory rename (atomic, no cross-volume copy).
	staged := filepath.Join(dir, binaryName+".new")
	_ = os.Remove(staged)
	if err := copyFile(target, staged); err != nil {
		return err
	}

	if _, err := os.Lstat(binPath); err == nil {
		retired := fmt.Sprintf("%s.old-%d", binPath, time.Now().UnixNano())
		if err := os.Rename(binPath, retired); err != nil {
			os.Remove(staged)
			return fmt.Errorf("failed retiring previous %s: %w", binPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		os.Remove(staged)
		return fmt.Errorf("failed inspecting %s: %w", binPath, err)
	}

	if err := os.Rename(staged, binPath); err != nil {
		os.Remove(staged)
		return fmt.Errorf("failed installing %s: %w", binPath, err)
	}

	sweepRetired(dir)
	return nil
}

// assertOwnedCopy returns nil iff binPath doesn't exist, or exists and its
// contents match one of the installed versions' binaries (i.e. it is a
// copy this tool made). Otherwise it returns ErrBinLinkConflict so we
// never destroy a file the user placed at the bin path themselves.
//
// We compare against the on-disk version binaries rather than the store's
// recorded checksum because the store records the tarball's sha256, not
// the extracted binary's.
func assertOwnedCopy(l Layout, store *Store, binPath string) error {
	binSum, err := fileSHA256(binPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed reading %s: %w", binPath, err)
	}
	for _, v := range store.Versions {
		versionSum, err := fileSHA256(l.VersionBinary(v.Version))
		if err != nil {
			// A recorded version whose binary is missing on disk can't be
			// the source of the bin copy; skip it.
			continue
		}
		if binSum == versionSum {
			return nil
		}
	}
	return fmt.Errorf(`%w:

%s already exists and was not created by `+"`hoop versions`"+`.

To recover, move or delete it and re-run the same command:
  - keep it:    move "%s" to another location
  - discard it: del "%s"`,
		ErrBinLinkConflict, binPath, binPath, binPath)
}

// sweepRetired best-effort removes binaries previously moved aside by
// copyActivate. The copy that belongs to a still-running process can't be
// deleted yet; it will be swept on a subsequent run after that process
// exits. Errors are intentionally ignored — a leftover file is harmless.
func sweepRetired(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, binaryName+".old-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// copyFile copies src to dst (0755), truncating dst if it exists. dst is
// removed on a partial copy so callers never observe a half-written file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed opening %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed creating %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("failed copying to %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("failed closing %s: %w", dst, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
