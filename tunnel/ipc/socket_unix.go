//go:build !windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// defaultClientSocketPath returns the OS-default control-plane address
// the client should dial when no override was supplied.
func defaultClientSocketPath() string { return DefaultSocketPathUnix }

// socketNetwork returns the net.Dial network string for the given
// address. On Unix everything is a "unix" socket; on Windows it's
// the named-pipe special-case.
func socketNetwork(_ string) string { return "unix" }

// platformListen brings up a Unix domain socket suitable for the local
// control plane.
//
// Steps:
//  1. Resolve the bind path (default DefaultSocketPathUnix).
//  2. Remove any stale socket file. We deliberately do not remove
//     regular files at the same path — that would mask configuration
//     mistakes like pointing at /etc/hosts.
//  3. net.Listen on the resolved path.
//  4. If GroupName is set, chown to root:<group> so members of that
//     group can connect.
//  5. Chmod to opts.Mode (default 0660).
//
// The function returns the listener; the caller is responsible for
// closing it on shutdown. The listener does NOT remove the socket file
// when closed — that's the binary's responsibility, because we want a
// missing socket file to be an explicit "daemon down" signal for the UI
// rather than a race with restart.
func platformListen(opts ListenerOptions) (net.Listener, error) {
	path := opts.Path
	if path == "" {
		path = DefaultSocketPathUnix
	}

	// Ensure the socket's parent directory exists before binding.
	//
	// On Linux this is normally provided by systemd's
	// `RuntimeDirectory=hsh` clause, but we create it here too so the
	// daemon also works when launched outside systemd (dev runs, or a
	// host where the directory was reaped). On macOS it is mandatory:
	// /var/run is on a volatile filesystem whose contents do not survive
	// a reboot, so the directory the installer created is gone by the
	// time the LaunchDaemon starts at next boot. launchd has no
	// RuntimeDirectory equivalent, so the daemon owns this.
	//
	// We chown the directory to the IPC group (when set) and give it
	// mode 0750 so members of that group can traverse into it to reach
	// the socket, matching the systemd unit's RuntimeDirectoryMode.
	if err := ensureSocketDir(path, opts.GroupName); err != nil {
		return nil, err
	}

	// Remove the path if (and only if) it is already a unix socket.
	// Anything else (regular file, directory) is treated as a hard
	// error so we never accidentally clobber a user's data.
	if st, err := os.Stat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("ipc: refusing to bind: %q exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("ipc: remove stale socket %q: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("ipc: stat %q: %w", path, err)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen unix %q: %w", path, err)
	}

	// Apply ownership/permissions only after Listen succeeds; if we
	// fail here we close the listener so we don't leak a fd or leave a
	// publicly-accessible socket lying around.
	if err := applySocketPerms(path, opts); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, err
	}

	return ln, nil
}

// ensureSocketDir creates the parent directory of the socket path (if
// missing) and, when groupName is set, chowns it to root:<group> with
// mode 0750 so group members can reach the socket inside it. Idempotent:
// an existing directory has its ownership/permissions re-asserted but is
// never removed. A failure to create the directory is fatal — without it
// net.Listen cannot bind.
//
// We deliberately do not MkdirAll arbitrary depth with loose
// permissions: only the immediate parent is created (the canonical
// /var/run/hsh under an existing /var/run), and it gets 0750 so the
// socket is not world-reachable even before the per-socket chmod runs.
func ensureSocketDir(socketPath, groupName string) error {
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("ipc: create socket dir %q: %w", dir, err)
	}
	if groupName != "" {
		grp, err := user.LookupGroup(groupName)
		if err != nil {
			return fmt.Errorf("ipc: lookup group %q: %w", groupName, err)
		}
		gid, err := strconv.Atoi(grp.Gid)
		if err != nil {
			return fmt.Errorf("ipc: parse gid for group %q: %w", groupName, err)
		}
		if err := os.Chown(dir, -1, gid); err != nil {
			return fmt.Errorf("ipc: chown socket dir %q to group %q: %w", dir, groupName, err)
		}
	}
	// Re-assert mode in case MkdirAll was a no-op (dir already existed
	// with a looser mode from a previous version) or the umask trimmed
	// our requested bits.
	if err := os.Chmod(dir, 0o750); err != nil {
		return fmt.Errorf("ipc: chmod socket dir %q: %w", dir, err)
	}
	return nil
}

// applySocketPerms enforces the configured GroupName + Mode on the
// freshly-created unix socket. Failures here are fatal because the
// alternative is exposing the socket to a wider audience than intended.
//
// Mode defaults to 0660 (owner+group rw). Anyone outside the group is
// kept out by filesystem permissions before they can even attempt a
// connect; the bearer-token check is the second line of defence.
func applySocketPerms(path string, opts ListenerOptions) error {
	mode := opts.Mode
	if mode == 0 {
		mode = 0o660
	}

	if opts.GroupName != "" {
		grp, err := user.LookupGroup(opts.GroupName)
		if err != nil {
			return fmt.Errorf("ipc: lookup group %q: %w", opts.GroupName, err)
		}
		gid, err := strconv.Atoi(grp.Gid)
		if err != nil {
			return fmt.Errorf("ipc: parse gid for group %q: %w", opts.GroupName, err)
		}
		// Chown to -1 (keep current uid) : gid. Using os.Chown directly
		// rather than syscall avoids OS-version variance.
		if err := os.Chown(path, -1, gid); err != nil {
			return fmt.Errorf("ipc: chown socket to group %q: %w", opts.GroupName, err)
		}
	}

	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("ipc: chmod socket %q to %o: %w", path, mode, err)
	}
	return nil
}
