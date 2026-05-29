//go:build windows

package ipc

import (
	"errors"
	"net"
)

// defaultClientSocketPath returns the OS-default control-plane address
// the client should dial when no override was supplied. Used by NewClient.
func defaultClientSocketPath() string { return DefaultSocketPathWindows }

// socketNetwork is unused on Windows because named-pipe dialing goes
// through a different stack (go-winio). Kept for API symmetry with the
// Unix build; if a future build wires up the Windows pipe transport,
// this helper will return the appropriate value.
func socketNetwork(_ string) string { return "unix" }

// platformListen on Windows is intentionally not implemented in this
// build. The control plane is functionally complete on Linux/macOS;
// Windows named-pipe support is tracked as a follow-up (the `hsh-tunneld`
// daemon already cross-compiles to windows/amd64 and windows/arm64 for
// release, but the IPC layer there is offline until a real Windows user
// surface lands).
//
// Returning an error here rather than panicking lets callers detect the
// situation cleanly and either fall back to env-var configuration (for
// the daemon in headless mode) or surface a clear "Windows IPC not
// implemented yet" message to the operator.
func platformListen(_ ListenerOptions) (net.Listener, error) {
	return nil, errors.New("ipc: Windows named-pipe listener not implemented in this build (see RD-215 follow-up)")
}
