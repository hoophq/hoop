//go:build !windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
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
