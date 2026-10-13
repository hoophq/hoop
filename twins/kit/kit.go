// Package kit is what every twin shares: the admin API (/_twin/seed,
// /_twin/state, /_twin/reset, /_twin/faults), failure injection in front of
// the twin's real surface, a request log, and the TLS listener.
//
// A twin implements Twin and nothing else. The kit owns the admin routes, so
// every twin answers them the same way.
package kit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Twin is a behavioral clone of one third-party service.
type Twin interface {
	// Name prefixes the request log.
	Name() string
	// Routes registers the surface hoop calls, on the real paths.
	Routes(mux *http.ServeMux)
	// Seed loads the state a scenario needs. It may be called more than once.
	Seed(raw json.RawMessage) error
	// State is what a scenario asserts on. It must be JSON-encodable.
	State() any
	// Reset drops all state, seeded or recorded.
	Reset()
}

// Actor is a twin that also acts as a user of the service (a reviewer who
// clicks in Slack). Its routes live under /_twin/act/.
type Actor interface {
	Actions(mux *http.ServeMux)
}

// Fault makes the twin misbehave on requests whose path starts with Path.
// Times bounds how many requests it hits; 0 means until reset.
type Fault struct {
	Path    string `json:"path"`
	Status  int    `json:"status,omitempty"`
	Body    string `json:"body,omitempty"`
	DelayMS int    `json:"delay_ms,omitempty"`
	Times   int    `json:"times,omitempty"`
	Hits    int    `json:"hits"`
}

// Handler serves the twin's surface behind failure injection, plus the admin
// API. Both listeners serve it, so a scenario can reach the admin API without
// TLS and the code under test reaches the surface with TLS.
func Handler(t Twin) http.Handler {
	h := &handler{twin: t, real: http.NewServeMux(), admin: http.NewServeMux()}
	t.Routes(h.real)
	h.admin.HandleFunc("POST /_twin/seed", h.seed)
	h.admin.HandleFunc("GET /_twin/state", h.state)
	h.admin.HandleFunc("POST /_twin/reset", h.reset)
	h.admin.HandleFunc("POST /_twin/faults", h.addFault)
	h.admin.HandleFunc("GET /_twin/healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
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
			log.Printf("[%s] %s %s%s -> %d (fault)", h.twin.Name(), r.Method, r.Host, r.URL.Path, f.Status)
			return
		}
	}
	h.real.ServeHTTP(rec, r)
	log.Printf("[%s] %s %s%s -> %d (%s)", h.twin.Name(), r.Method, r.Host, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
}

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
	log.Printf("[%s] seeded", h.twin.Name())
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
	log.Printf("[%s] reset", h.twin.Name())
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) addFault(w http.ResponseWriter, r *http.Request) {
	var f Fault
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil || f.Path == "" {
		http.Error(w, fmt.Sprintf("want {path, status|delay_ms, times}, err=%v", err), http.StatusBadRequest)
		return
	}
	f.Hits = 0
	h.mu.Lock()
	h.faults = append(h.faults, &f)
	h.mu.Unlock()
	log.Printf("[%s] fault on %s: status=%d delay=%dms times=%d", h.twin.Name(), f.Path, f.Status, f.DelayMS, f.Times)
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

// Hijack hands the connection to the Socket Mode websocket upgrade.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	s.status = http.StatusSwitchingProtocols
	return http.NewResponseController(s.ResponseWriter).Hijack()
}
