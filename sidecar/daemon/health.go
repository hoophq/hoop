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

// handleHealth mounts /healthz and /readyz on mux. The admin listener and the
// health listener share it, so a probe gets the same answer from either.
func handleHealth(mux *http.ServeMux, endpoints []endpointServer, endpointNames []string) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// An ica lane's engine loads a model and starts OCR workers after the
	// socket binds, which can take minutes, and the engine refuses
	// connections until then. /readyz reports that, separate from
	// /healthz; other lanes are ready once bound, so it returns 200 for them.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if notReady := notReadyEndpoints(endpoints, endpointNames); len(notReady) > 0 {
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
func serveHealth(ctx context.Context, addr string, endpoints []endpointServer, endpointNames []string, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("health listener (%s): %w", HealthListenEnv, err)
	}
	mux := http.NewServeMux()
	handleHealth(mux, endpoints, endpointNames)
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
