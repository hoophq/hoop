package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// HealthListenEnv is the address of the health listener: GET /healthz and
// GET /readyz, and nothing else. Unset, no health listener binds.
//
// It exists so a platform can probe the process with the admin listener off.
// The admin listener serves the audit trail and each lane's rules without
// authentication, so a workspace that can reach it can read them. The health
// listener answers only whether the process is up and its lanes are ready.
//
// An environment variable and never a config key: the probe belongs to the
// deployment that declares it, not to a document a control plane serves.
const HealthListenEnv = "HOOP_SIDECAR_HEALTH_LISTEN"

// checkHealthListen refuses a health address that is not host:port, or whose
// port another listener of cfg also binds: a lane, a lane's ICA slot range,
// the admin server or the MCP server. Run calls it before anything binds, so
// the operator reads the conflict instead of a bind error from whichever
// listener lost the race.
func checkHealthListen(cfg *Config, addr string) error {
	if addr == "" {
		return nil
	}
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %q: want host:port: %w", HealthListenEnv, addr, err)
	}
	port, err := net.LookupPort("tcp", p)
	if err != nil {
		return fmt.Errorf("%s %q: %q is not a port number", HealthListenEnv, addr, p)
	}
	if port == 0 {
		return nil
	}
	clash := func(what string) error {
		return fmt.Errorf("%s %q: port %d is also %s; pick another port", HealthListenEnv, addr, port, what)
	}
	mcpListen := ""
	if cfg.MCP != nil {
		mcpListen = cfg.MCP.Listen
	}
	for what, other := range map[string]string{
		"the admin listener (admin.listen)": cfg.Admin.Listen,
		"the MCP server (mcp.listen)":       mcpListen,
	} {
		if oh, n, ok := listenHostPort(other); ok && n == port && sameBindHost(host, oh) {
			return clash(what)
		}
	}
	for i, l := range cfg.Listeners {
		if l.Network == "unix" {
			continue
		}
		lh, first, ok := listenHostPort(l.listenAddress())
		if !ok || !sameBindHost(host, lh) {
			continue
		}
		last := first
		// Slot mode binds first_port through first_port+count-1 on the
		// lane's host; Validate makes listen name first_port.
		if l.ICA != nil && l.ICA.Slots != nil {
			first, last = l.ICA.Slots.FirstPort, l.ICA.Slots.FirstPort+l.ICA.Slots.Count-1
		}
		if port >= first && port <= last {
			return clash("bound by " + l.displayName(i))
		}
	}
	return nil
}

// listenHostPort splits a host:port and resolves the port the way net.Listen
// does: a service name or a zero-padded number names the same port as its
// decimal.
func listenHostPort(addr string) (string, int, bool) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, false
	}
	n, err := net.LookupPort("tcp", p)
	return h, n, err == nil && n != 0
}

// sameBindHost reports whether two listeners on one port would collide: when
// either binds every address, or both name the same one. Two distinct local
// addresses can share a port.
func sameBindHost(a, b string) bool {
	wildcard := func(h string) bool {
		ip := net.ParseIP(h)
		return h == "" || (ip != nil && ip.IsUnspecified())
	}
	if wildcard(a) || wildcard(b) {
		return true
	}
	if ia, ib := net.ParseIP(a), net.ParseIP(b); ia != nil && ib != nil {
		return ia.Equal(ib)
	}
	return strings.EqualFold(a, b)
}

// healthState is what /readyz reads. The admin listener and the health
// listener build their routes from one value, so a probe gets the same answer
// from either.
type healthState struct {
	endpoints     []endpointServer
	endpointNames []string
	// icaReadinessJSON is admin.ica_readiness_json: /readyz answers with the
	// ICA inspection attestation instead of plain text.
	icaReadinessJSON bool
	proxyCount       int
}

// handleHealth mounts /healthz and /readyz on mux.
func handleHealth(mux *http.ServeMux, h healthState) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// An ica lane's engine loads a model and starts OCR workers after the
	// socket binds, which can take minutes, and the engine refuses
	// connections until then. /readyz reports that, separate from
	// /healthz; other lanes are ready once bound, so it returns 200 for them.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if h.icaReadinessJSON {
			writeICAReadiness(w, h.endpoints, h.proxyCount)
			return
		}
		if notReady := notReadyEndpoints(h.endpoints, h.endpointNames); len(notReady) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "not ready: %s\n", strings.Join(notReady, ", "))
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ready")
	})
}

// serveHealth binds addr and serves the health routes until ctx ends. A bind
// failure is returned: the platform probes this address, and a process it
// cannot probe gets restarted in a loop.
func serveHealth(ctx context.Context, addr string, h healthState, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("health listener (%s): %w", HealthListenEnv, err)
	}
	mux := http.NewServeMux()
	handleHealth(mux, h)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("health endpoint listening", "listen", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("health listener: %w", err)
	}
	return nil
}
