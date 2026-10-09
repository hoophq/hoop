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

// Addr is where the demo API prefers to listen: loopback, beside the demo
// listener that fronts it. The setup screen moves both to free ports when
// another program holds these.
const Addr = "127.0.0.1:18081"

// ListenAddr is where the demo listener prefers to accept clients.
const ListenAddr = "127.0.0.1:18080"

// Ports are where one demo actually runs: the sidecar listener clients
// send to, and the API behind it.
type Ports struct{ Listen, API string }

// DefaultPorts are the preferred addresses.
var DefaultPorts = Ports{Listen: ListenAddr, API: Addr}

// Step is one beat of the guided demo: a request, what to look for in the
// answer, and what the sidecar did to produce it.
type Step struct {
	Title        string
	Method, Path string
	Body         string
	// Direct sends the request to the API itself, around the sidecar, so
	// the person can compare.
	Direct bool
	// Expect is what the answer shows; Why is what the sidecar did.
	Expect, Why string
	// SeenKind and SeenOp name the audit event the step produces, so a
	// request sent from another terminal ticks the step too. Empty for a
	// direct request, which the sidecar never sees.
	SeenKind, SeenOp string
}

// URL is where the step sends its request.
func (s Step) URL(p Ports) string {
	host := p.Listen
	if s.Direct {
		host = p.API
	}
	return "http://" + host + s.Path
}

// Curl is the step as a command to paste into another terminal.
func (s Step) Curl(p Ports) string {
	c := "curl -s"
	if s.Method != "GET" {
		c += " -X " + s.Method
	}
	if s.Body != "" {
		c += " -H 'content-type: application/json' -d '" + s.Body + "'"
	}
	return c + " " + s.URL(p)
}

// Steps is the guided demo, in the order a person should take it.
var Steps = []Step{
	{
		Title: "See masking", Method: "GET", Path: "/users",
		Expect:   "Every email comes back as [REDACTED:EMAIL_ADDRESS].",
		Why:      "The masking rule \"emails\" found each address in the response and rewrote it before the client saw it.",
		SeenKind: "masked",
	},
	{
		Title: "See a guardrail", Method: "DELETE", Path: "/users/1",
		Expect:   "403: the request never reaches the API.",
		Why:      "The guardrail \"no-deletes\" matched the DELETE operation and the sidecar answered for the API.",
		SeenKind: "violation", SeenOp: "delete",
	},
	{
		Title: "Write through it", Method: "POST", Path: "/users",
		Body:     `{"name":"Ada Lovelace","email":"ada@example.com"}`,
		Expect:   "201 with the new user, its email masked on the way back.",
		Why:      "Allowed writes pass through and are recorded in the audit trail like every statement.",
		SeenKind: "statement", SeenOp: "post",
	},
	{
		Title: "Compare: no sidecar", Method: "GET", Path: "/users/2", Direct: true,
		Expect: "The real email address, because this request went around the sidecar.",
		Why:    "This request went straight to the API. Only traffic sent to the sidecar listener is inspected.",
	},
}

// TryCommands are the steps as commands, for the config header and the
// dashboard notes.
func TryCommands(p Ports) []string {
	out := make([]string, 0, len(Steps))
	for _, s := range Steps {
		out = append(out, s.Curl(p))
	}
	return out
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
			"try":   "send requests through the sidecar listener in front of this API",
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
