// Package kit is what every twin shares (ADR-0032): the admin API, failure
// injection in front of the twin's surface, a request log, and the TLS and
// admin listeners.
//
// A twin implements Twin. The kit owns /_twin/seed, /_twin/state,
// /_twin/reset, /_twin/faults, /_twin/healthz and /_twin/ui, so every twin
// answers them the same way.
//
// Twins import nothing from github.com/hoophq/hoop (ADR-0032), so they log
// with log/slog, not common/log.
package kit

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Twin is a behavioral clone of one third-party service.
type Twin interface {
	// Name prefixes the request log and the state document.
	Name() string
	// Routes registers the surface hoop calls, on the real paths.
	Routes(mux *http.ServeMux)
	// Seed loads the state a scenario needs. A scenario can call it more
	// than once; each call adds to or replaces what the last one set.
	Seed(raw json.RawMessage) error
	// State is what a scenario asserts on. It must encode to JSON.
	State() any
	// Reset drops all state, seeded or recorded.
	Reset()
}

// Actor is a twin that also acts as a user of the service, for example a
// reviewer who clicks in Slack. Its routes live under /_twin/act/.
type Actor interface {
	Actions(mux *http.ServeMux)
}

// Fault makes the twin misbehave on requests whose path starts with Path.
// Status writes that status and Body instead of the real answer. DelayMS
// holds the request first; with no Status, the real answer follows the
// delay. Times bounds how many requests it hits; 0 means until cleared.
type Fault struct {
	Path    string `json:"path"`
	Status  int    `json:"status,omitempty"`
	Body    string `json:"body,omitempty"`
	DelayMS int    `json:"delay_ms,omitempty"`
	Times   int    `json:"times,omitempty"`
	Hits    int    `json:"hits"`
}

func (f Fault) validate() error {
	switch {
	case !strings.HasPrefix(f.Path, "/") || strings.HasPrefix(f.Path, "/_twin/"):
		return fmt.Errorf("path must start with / and must not be an admin path, got %q", f.Path)
	case f.Status == 0 && f.DelayMS <= 0:
		return fmt.Errorf("a fault needs a status, a delay_ms, or both")
	case f.Status != 0 && (f.Status < 100 || f.Status > 599):
		return fmt.Errorf("status %d is not an HTTP status", f.Status)
	case f.Times < 0 || f.DelayMS < 0:
		return fmt.Errorf("times and delay_ms must not be negative")
	}
	return nil
}

// Handler serves the twin's surface behind failure injection, plus the admin
// API. Both listeners serve it: a scenario reaches the admin API with no TLS,
// and the code under test reaches the surface with TLS.
func Handler(t Twin) http.Handler {
	h := &handler{twin: t, real: http.NewServeMux(), admin: http.NewServeMux()}
	t.Routes(h.real)
	h.admin.HandleFunc("POST /_twin/seed", h.seed)
	h.admin.HandleFunc("GET /_twin/state", h.state)
	h.admin.HandleFunc("POST /_twin/reset", h.reset)
	h.admin.HandleFunc("POST /_twin/faults", h.addFault)
	h.admin.HandleFunc("DELETE /_twin/faults", h.clearFaults)
	h.admin.HandleFunc("GET /_twin/healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	h.admin.HandleFunc("GET /_twin/ui", h.ui)
	if a, ok := t.(Actor); ok {
		a.Actions(h.admin)
	}
	return h
}

type handler struct {
	twin  Twin
	real  *http.ServeMux
	admin *http.ServeMux

	mu     sync.Mutex
	faults []*Fault
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_twin/") {
		h.admin.ServeHTTP(w, r)
		return
	}
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	start := time.Now()
	if f := h.fault(r.URL.Path); f != nil {
		if f.DelayMS > 0 {
			time.Sleep(time.Duration(f.DelayMS) * time.Millisecond)
		}
		if f.Status != 0 {
			rec.WriteHeader(f.Status)
			io.WriteString(rec, f.Body)
			slog.Info("request", "twin", h.twin.Name(), "method", r.Method, "host", r.Host, "path", r.URL.Path,
				"status", f.Status, "fault", true)
			return
		}
	}
	h.real.ServeHTTP(rec, r)
	slog.Info("request", "twin", h.twin.Name(), "method", r.Method, "host", r.Host, "path", r.URL.Path,
		"status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
}

// fault returns a copy of the first fault that matches path and counts the
// hit, or nil.
func (h *handler) fault(path string) *Fault {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.faults {
		if !strings.HasPrefix(path, f.Path) || (f.Times > 0 && f.Hits >= f.Times) {
			continue
		}
		f.Hits++
		c := *f
		return &c
	}
	return nil
}

func (h *handler) seed(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.twin.Seed(raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Info("seeded", "twin", h.twin.Name())
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) state(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	faults := make([]Fault, 0, len(h.faults))
	for _, f := range h.faults {
		faults = append(faults, *f)
	}
	h.mu.Unlock()
	WriteJSON(w, http.StatusOK, map[string]any{"twin": h.twin.Name(), "state": h.twin.State(), "faults": faults})
}

func (h *handler) reset(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	h.faults = nil
	h.mu.Unlock()
	h.twin.Reset()
	slog.Info("reset", "twin", h.twin.Name())
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) addFault(w http.ResponseWriter, r *http.Request) {
	var f Fault
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		http.Error(w, "want {path, status, body, delay_ms, times}: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := f.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.Hits = 0
	h.mu.Lock()
	h.faults = append(h.faults, &f)
	h.mu.Unlock()
	slog.Info("fault added", "twin", h.twin.Name(), "path", f.Path, "status", f.Status, "delay_ms", f.DelayMS, "times", f.Times)
	w.WriteHeader(http.StatusNoContent)
}

// clearFaults drops every fault and keeps the seeded state.
func (h *handler) clearFaults(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	h.faults = nil
	h.mu.Unlock()
	slog.Info("faults cleared", "twin", h.twin.Name())
	w.WriteHeader(http.StatusNoContent)
}

// WriteJSON writes v as the JSON body.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
