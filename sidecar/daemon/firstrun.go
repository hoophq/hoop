// First-run mode: what a bare `hoop start sidecar` or `hoop-inspect` does
// when the operator supplied nothing at all — no config, no control plane,
// no flags. It used to be a usage error, and a new user's first contact with
// the product was that error. Now it is a working loopback URL and a next
// step.
//
// This is NOT a relay lane. No codec runs, no policy evaluates, no audit row
// is written, and the "an empty config cannot start" rule for real configs
// (see resolveConfigSource) stands untouched: first-run is a separate path
// that engages only when there is no config to resolve. The lane answers
// every request with a redirect to the live getting-started guide — the
// docs stay on the docs site, nothing is hosted here.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// FirstRunDocsURL is where the default lane sends a browser. The site
// resolves the root to the getting-started guide; the deep URL under it is
// the docs site's to rearrange, so the redirect names the root that will
// not rot.
const FirstRunDocsURL = "https://hoop.dev/docs"

// firstRunAddr is the preferred bind: loopback only, and a port unusual
// enough that a laptop's usual suspects (5432, 8080, 3000) and our own
// conventions (15432 for the demo Postgres lane, 19000 for /stats) never
// sit on it. A preference, not a promise: firstRunListen falls back when
// something holds it, and the banner prints what was actually bound.
const firstRunAddr = "127.0.0.1:15321"

// firstRunFallbackAddr lets the kernel assign a free port when the
// preferred one is taken. No scan-for-the-next-port loop: between probing
// a port and binding it somebody else can take it, and :0 has no such
// window.
const firstRunFallbackAddr = "127.0.0.1:0"

// FirstRun binds one loopback HTTP listener, redirects every request to the
// getting-started guide, prints where it bound and what to do next, and
// blocks until SIGINT or SIGTERM, like Run.
//
// restartCmd is the command line the banner tells the user to run once they
// have written a config; the CLI and the standalone binary spell it
// differently.
func FirstRun(out io.Writer, restartCmd string) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	return firstRunServe(ctx, out, restartCmd)
}

// firstRunServe is FirstRun minus the signal wiring, so a test can stop it
// with a context instead of a signal.
func firstRunServe(ctx context.Context, out io.Writer, restartCmd string) error {
	ln, fellBack, err := firstRunListen()
	if err != nil {
		return err
	}

	firstRunBanner(out, ln.Addr().String(), fellBack, restartCmd)

	srv := &http.Server{
		Handler:           firstRunRedirect(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errc:
		return err
	}
}

// firstRunListen binds the preferred address, or a kernel-assigned free
// port when it is taken. fellBack reports which one happened, so the banner
// can say the port moved.
func firstRunListen() (ln net.Listener, fellBack bool, err error) {
	return firstRunListenAt(firstRunAddr, firstRunFallbackAddr)
}

// firstRunListenAt is firstRunListen over caller-chosen addresses, so a
// test can exercise the error classification without squatting on the
// real port.
//
// Only a busy preferred port moves to the fallback: that is the one
// failure a different port fixes. Anything else — no route, fd
// exhaustion, a policy denying the bind — would fail again on the
// fallback, and retrying would misreport it as "port busy" in the
// banner, so it is returned as what it is. syscall.EADDRINUSE matches
// both the bare errno and the wrapped net.OpError forms Go returns,
// on POSIX and Windows alike (see tunnel/loginflow's isAddrInUse).
func firstRunListenAt(preferred, fallback string) (ln net.Listener, fellBack bool, err error) {
	ln, err = net.Listen("tcp", preferred)
	if err == nil {
		return ln, false, nil
	}
	if !errors.Is(err, syscall.EADDRINUSE) {
		return nil, false, fmt.Errorf("binding the default listener: %w", err)
	}
	ln, fbErr := net.Listen("tcp", fallback)
	if fbErr != nil {
		return nil, false, fmt.Errorf("the preferred address %s is busy and the fallback bind failed: %w",
			preferred, fbErr)
	}
	return ln, true, nil
}

// firstRunRedirect answers every path with a redirect to the guide.
//
// 302, never 301: a browser caches a 301 per host:port, and the next
// process to bind this port on the same machine would inherit the redirect.
func firstRunRedirect() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, FirstRunDocsURL, http.StatusFound)
	})
}

// firstRunBanner is the whole first-run interface: one URL to open, the
// fact that this is a default rather than a configuration, and the command
// that replaces it. Prose to stdout on purpose — the operational JSON log
// is for a sidecar that is running a config, and this mode exists for a
// human at a terminal who has not written one yet.
func firstRunBanner(out io.Writer, addr string, fellBack bool, restartCmd string) {
	_, _ = fmt.Fprintf(out, "No config file given. Running with a built-in default.\n\n")
	if fellBack {
		_, _ = fmt.Fprintf(out, "  The default port (%s) was busy; a free one was bound instead.\n\n",
			portOf(firstRunAddr))
	}
	_, _ = fmt.Fprintf(out, "  Open http://%s in your browser.\n", addr)
	_, _ = fmt.Fprintf(out, "  It forwards to the getting-started guide at %s.\n\n", FirstRunDocsURL)
	_, _ = fmt.Fprintf(out, "This default inspects no traffic. Write a config with your first\n")
	_, _ = fmt.Fprintf(out, "listener and restart:\n\n")
	_, _ = fmt.Fprintf(out, "  %s\n", restartCmd)
}

// portOf extracts the port from a host:port literal, for the banner's
// port-was-busy line. The input is a package constant, so a split failure
// is a compile-time-shaped bug; returning the input whole keeps the banner
// printable anyway.
func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}
