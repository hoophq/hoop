package ipc

import (
	"net"
	"os"
)

// DefaultSocketPath is the platform-default location the daemon binds
// its control plane to. Tests and dev invocations can override via
// ListenerOptions.Path.
const (
	// DefaultSocketPathUnix is the production Unix-socket path for
	// hsh-tunneld. /var/run/hsh/ is the canonical home for the daemon's
	// runtime artifacts; systemd's `RuntimeDirectory=hsh` clause in the
	// service unit creates it at activation time with the right
	// ownership and persists nothing across reboots (which is exactly
	// what we want for a transient socket + token).
	//
	// On macOS, /var/run is created by launchd at boot and is writable
	// by root; the LaunchDaemon installer makes /var/run/hsh/ itself.
	//
	// We keep the socket inside that same directory (not at /var/run/hsh.sock
	// at the parent) so a single chown of /var/run/hsh/ is enough to
	// authorise the hsh group on both the socket and the control-token
	// file that lives next to it.
	DefaultSocketPathUnix = "/var/run/hsh/hsh.sock"

	// DefaultSocketPathWindows is the production named-pipe path for
	// hsh-tunneld. \\.\pipe\ is the only legal namespace for named
	// pipes on Windows; the file system layer does not apply.
	DefaultSocketPathWindows = `\\.\pipe\hsh`
)

// ListenerOptions configures how the control-plane listener is created.
// The zero value gives the production defaults on the current OS.
type ListenerOptions struct {
	// Path overrides the OS default socket / pipe path. Useful for
	// tests (which write to a temp dir) and for dev runs where the
	// daemon does not have permission to write to /var/run.
	//
	// On Unix this must be an absolute filesystem path that the daemon
	// can write to; the parent directory is NOT created automatically.
	// On Windows it must look like `\\.\pipe\<name>`.
	Path string

	// GroupName is the OS group that owns the Unix socket. Members of
	// this group can connect to the daemon; everyone else gets
	// permission denied at the OS layer (before the bearer-token check
	// even runs). Empty means "leave the socket owned by the process
	// gid, no chown". Ignored on Windows.
	GroupName string

	// Mode is the unix-socket file mode applied after Listen. Defaults
	// to 0660 (owner+group rw). Ignored on Windows.
	Mode os.FileMode
}

// Listen creates the local-only listener for the control plane. The
// platform-specific implementations live in socket_unix.go and
// socket_windows.go; this file holds the shared types.
//
// On Unix, Listen also removes any stale socket file at Path so a
// crashed daemon does not leave the address blocked. The cleanup is
// safe because both kinds of leftover files (regular file or stale
// socket) are owned by root.
//
// On Windows, Listen creates a named pipe. Path defaults differ from
// Unix (see DefaultSocketPathWindows).
//
// Callers are responsible for closing the returned net.Listener on
// shutdown. They are also responsible for unlinking the socket file
// on shutdown if they care about leftover artifacts (the kernel does
// not GC unix sockets when the listener closes).
func Listen(opts ListenerOptions) (net.Listener, error) {
	return platformListen(opts)
}
