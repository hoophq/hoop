// Package sidecardemo serves the demo API the first-run screen's "Demo"
// choice puts behind a sidecar: a small JSON API over fake users, so a person
// with no database at hand can watch the sidecar mask an email and refuse a
// DELETE. Every value in it is invented.
package sidecardemo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Addr is where the demo API listens: loopback, beside the demo listener
// (18080) that fronts it.
const Addr = "127.0.0.1:18081"

// ListenAddr is where the demo config's http listener accepts clients.
const ListenAddr = "127.0.0.1:18080"

// TryCommands are what a person runs against the demo listener to see each
// feature act. Shown on the first-run screen and written into the config.
var TryCommands = []string{
	"curl -s http://" + ListenAddr + "/users            # emails come back masked",
	"curl -s -X DELETE http://" + ListenAddr + "/users/1  # refused by the guardrail",
	"curl -s -X POST http://" + ListenAddr + "/users -d '{\"name\":\"Ada\",\"email\":\"ada@example.com\"}'",
}

type user struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone,omitempty"`
	Card  string `json:"card,omitempty"`
	Plan  string `json:"plan"`
}

func seed() []user {
	return []user{
		{1, "Grace Hopper", "grace.hopper@example.com", "+1 202 555 0143", "4111 1111 1111 1111", "enterprise"},
		{2, "Alan Turing", "alan.turing@example.org", "+44 20 7946 0958", "5500 0000 0000 0004", "team"},
		{3, "Katherine Johnson", "kjohnson@example.net", "+1 757 555 0199", "3400 0000 0000 009", "free"},
		{4, "Linus Torvalds", "linus@example.com", "+358 40 555 0175", "", "free"},
	}
}

// Server is the demo API. Its state lives in memory and resets per process.
type Server struct {
	mu    sync.Mutex
	users []user
	next  int
}

// NewServer returns a demo API holding the seed users.
func NewServer() *Server {
	s := &Server{users: seed()}
	s.next = len(s.users) + 1
	return s
}

// Serve listens on addr until ctx ends. A port already taken is returned at
// once: booting a demo whose upstream is someone else's server would show
// that server's data under the demo's name.
func Serve(ctx context.Context, addr string) (<-chan error, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("the demo API cannot listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: NewServer().Handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	return done, nil
}

// Handler routes the demo API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"about": "hoop sidecar demo API: invented users behind an inspecting sidecar",
			"try":   TryCommands,
		})
	})
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, http.StatusOK, s.users)
	})
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if i := s.find(r.PathValue("id")); i >= 0 {
			writeJSON(w, http.StatusOK, s.users[i])
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such user"})
	})
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var u user
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&u); err != nil || strings.TrimSpace(u.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `send {"name": "...", "email": "..."}`})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		u.ID, u.Plan = s.next, "free"
		s.next++
		s.users = append(s.users, u)
		writeJSON(w, http.StatusCreated, u)
	})
	mux.HandleFunc("DELETE /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := s.find(r.PathValue("id"))
		if i < 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such user"})
			return
		}
		s.users = append(s.users[:i], s.users[i+1:]...)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (s *Server) find(id string) int {
	n, err := strconv.Atoi(id)
	if err != nil {
		return -1
	}
	for i, u := range s.users {
		if u.ID == n {
			return i
		}
	}
	return -1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
