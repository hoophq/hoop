package loginflow

import (
	"context"
	"net"
	"syscall"
)

// defaultListen is a thin var indirection over net.Listen so tests can
// stub it. Production code passes the real network/address through; the
// callback server always uses "tcp" because the OAuth redirect comes
// from a real browser regardless of platform.
//
// We set SO_REUSEADDR on the listening socket so a fresh `Start` after
// a recent `Cancel` (the user Ctrl-Cs the login then re-runs `hsh
// tunnel login` within a minute, or the OAuth flow times out and they
// retry) doesn't trip TIME_WAIT. Without it the second bind fails
// with "address already in use" for ~60s after the first listener
// closes — a real user-visible bug in the production flow that also
// happened to be the cause of CI flakes in TestCancel.
//
// SO_REUSEADDR semantics on Linux/macOS:
//
//   - On a listening socket, it permits binding to a 127.0.0.1:<port>
//     pair where a previous socket on the same pair is in TIME_WAIT.
//     It does NOT allow two active listeners on the same address; that
//     would need SO_REUSEPORT and is explicitly not what we want.
//
//   - The fixed-port (127.0.0.1:3587) production address is what the
//     gateway-side OAuth redirect points at, so we must reuse exactly
//     the same address. SO_REUSEADDR is the right knob.
//
// Windows behaviour differs (SO_REUSEADDR there permits two active
// binds, which is the opposite of what we want). For Windows we skip
// the option entirely — the only consumer there is the unit test
// suite running on a Windows CI runner, where 60s of TIME_WAIT is
// the OS contract and re-running the test sequentially is the
// expected fix. The production callback server isn't supported on
// Windows yet anyway (RD-217 follow-up).
var defaultListen = func(network, addr string) (net.Listener, error) {
	lc := net.ListenConfig{Control: setReuseAddr}
	return lc.Listen(context.Background(), network, addr)
}

// setReuseAddr is the syscall.RawConn callback that ListenConfig
// hands us. It runs after socket() but before bind(), which is the
// only window in which SO_REUSEADDR is meaningful.
//
// On platforms where the option is unsupported or semantically
// different (Windows), this becomes a no-op via the build-tag-gated
// implementations in listen_unix.go / listen_windows.go.
func setReuseAddr(network, address string, c syscall.RawConn) error {
	return applyReuseAddr(c)
}
