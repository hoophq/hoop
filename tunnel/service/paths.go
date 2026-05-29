//go:build linux || darwin

package service

// This file holds the POSIX filesystem helpers shared by the Linux
// (systemd) and macOS (launchd) Manager implementations. They are
// platform-neutral within POSIX — root-owned files, a `<group>` group
// read via os/user, temp-file-plus-rename atomic writes — so keeping a
// single copy avoids the two backends drifting in their idempotency or
// permission behaviour.
//
// Windows has its own (registry/ACL-based) equivalents and does not
// build this file.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
)

// ensureConfigDirAndFile creates /etc/hsh/ (or the parent of
// configPath) and writes an empty config.toml if one is not already
// there. The directory gets mode 0750 and is chowned to root:groupName
// so the daemon (root) and the hsh group can both read it. The file
// itself stays mode 0600 — only root needs to read the token.
//
// Existing files are left untouched. This lets a reinstall preserve
// the user's saved token without an explicit migration.
func ensureConfigDirAndFile(configPath, groupName string) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	// chown the dir to root:hsh. LookupGroup may return ErrNoGroup if
	// the install is racing CreateGroup=false + an external packager
	// — in that case skip the chown (the operator can fix it later)
	// rather than fail the entire install.
	if grp, err := user.LookupGroup(groupName); err == nil {
		var gid int
		fmt.Sscanf(grp.Gid, "%d", &gid)
		if err := os.Chown(dir, 0, gid); err != nil {
			return fmt.Errorf("chown %q to root:%s: %w", dir, groupName, err)
		}
	}

	// Touch config.toml so validate-config has something to parse.
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(configPath, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("create %q: %w", configPath, err)
		}
		if _, err := f.WriteString("# hsh-tunneld config — managed by `hsh tunnel config`\n"); err != nil {
			_ = f.Close()
			return fmt.Errorf("write %q: %w", configPath, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %q: %w", configPath, err)
		}
		// Belt and braces: re-chmod in case the umask gave us 0644.
		if err := os.Chmod(configPath, 0600); err != nil {
			return fmt.Errorf("chmod %q: %w", configPath, err)
		}
	}
	return nil
}

// copyExecutable copies src to dst with mode 0755 and root:root
// ownership (we are already running as root). It is safe to copy onto
// the running binary's own path because we copy through a temp file
// + rename — the same trick the Go updater uses.
func copyExecutable(src, dst string) error {
	if src == dst {
		// Already where we want it (e.g. a packager-supplied install
		// where the user happened to be running the installed binary).
		// Just ensure the mode bits are right.
		return os.Chmod(dst, 0755)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer in.Close()

	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create %q: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q -> %q: %w", tmp, dst, err)
	}
	return nil
}

// writeFileIfDifferent writes contents to path if (a) the file does
// not exist or (b) its current contents differ. Returns (changed, err).
//
// Skipping the write when the contents match means re-running
// `hsh-tunneld install` does not bump the mtime of the unit/plist file
// (and therefore does not trigger a reload), which is the cheap-fast
// path for an idempotent reinstall.
func writeFileIfDifferent(path string, contents []byte, mode os.FileMode) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(contents) {
			return false, nil
		}
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, contents, mode); err != nil {
		return false, fmt.Errorf("write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("rename %q -> %q: %w", tmp, path, err)
	}
	return true, nil
}
