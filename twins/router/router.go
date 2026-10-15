// Package router is the HTTPS_PROXY that a scenario points the code under
// test at (ADR-0032, reach option B). It answers CONNECT by the requested
// hostname: it tunnels a mapped host to its twin, and refuses and records
// any other host. TLS stays end to end between the client and the twin,
// which shows a test-CA certificate for the requested hostname.
package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/twins/kit"
)

// Tunnel is one CONNECT the router answered.
type Tunnel struct {
	Host    string    `json:"host"`
	Target  string    `json:"target,omitempty"`
	Allowed bool      `json:"allowed"`
	At      time.Time `json:"at"`
	// Error is how an allowed tunnel broke, when it did not end with a close.
	Error string `json:"error,omitempty"`
}

// Router maps hostnames to twins. It implements kit.Twin for its admin API.
type Router struct {
	mu      sync.Mutex
	routes  map[string]string // hostname -> host:port of the twin
	tunnels []*Tunnel
}

// New takes host=target pairs, for example "oidc-twin=127.0.0.1:18441".
func New(pairs []string) (*Router, error) {
	r := &Router{routes: map[string]string{}}
	for _, p := range pairs {
		host, target, ok := strings.Cut(p, "=")
		if !ok || host == "" || target == "" {
			return nil, fmt.Errorf("route %q is not host=target", p)
		}
		r.routes[host] = target
	}
	return r, nil
}

func (r *Router) Name() string { return "router" }

func (r *Router) Routes(*http.ServeMux) {}

// Seed adds routes: {"routes": {"host": "target"}}.
func (r *Router) Seed(raw json.RawMessage) error {
	var s struct {
		Routes map[string]string `json:"routes"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for h, t := range s.Routes {
		r.routes[h] = t
	}
	return nil
}

func (r *Router) State() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	routes := make(map[string]string, len(r.routes))
	for h, t := range r.routes {
		routes[h] = t
	}
	tunnels := make([]Tunnel, 0, len(r.tunnels))
	for _, t := range r.tunnels {
		tunnels = append(tunnels, *t)
	}
	return map[string]any{"routes": routes, "tunnels": tunnels, "refused": r.refused()}
}

// refused counts refused CONNECTs by host. The caller holds r.mu.
func (r *Router) refused() []map[string]any {
	n := map[string]int{}
	for _, t := range r.tunnels {
		if !t.Allowed {
			n[t.Host]++
		}
	}
	hosts := make([]string, 0, len(n))
	for h := range n {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	out := make([]map[string]any, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, map[string]any{"host": h, "count": n[h]})
	}
	return out
}

// Reset forgets the tunnel log and keeps the routes, which come from flags.
func (r *Router) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tunnels = nil
}

// Handler is the proxy on CONNECT and the kit admin API otherwise.
func (r *Router) Handler() http.Handler {
	admin := kit.Handler(r)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			admin.ServeHTTP(w, req)
			return
		}
		r.connect(w, req)
	})
}

func (r *Router) connect(w http.ResponseWriter, req *http.Request) {
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
	}
	r.mu.Lock()
	target, ok := r.routes[host]
	tunnel := &Tunnel{Host: req.Host, Target: target, Allowed: ok, At: time.Now().UTC()}
	r.tunnels = append(r.tunnels, tunnel)
	r.mu.Unlock()
	if !ok {
		slog.Warn("connect refused: no twin for this host", "host", req.Host)
		http.Error(w, "no twin for "+host, http.StatusForbidden)
		return
	}
	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		r.fail(tunnel, fmt.Errorf("dial %s: %w", target, err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upstream.Close()
		r.fail(tunnel, fmt.Errorf("hijack: %w", err))
		return
	}
	slog.Info("connect", "host", req.Host, "target", target)
	errc := make(chan error, 2)
	go func() {
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			errc <- err
			return
		}
		// The client can send its TLS hello in the same packet as CONNECT.
		if n := buf.Reader.Buffered(); n > 0 {
			b, _ := buf.Reader.Peek(n) // n bytes are buffered, so Peek cannot fail
			if _, err := upstream.Write(b); err != nil {
				errc <- err
				return
			}
		}
		_, err := io.Copy(upstream, client)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(client, upstream)
		errc <- err
	}()
	// The first direction to end ends the tunnel. The other one then sees
	// the close, which is not a failure.
	err = <-errc
	client.Close()
	upstream.Close()
	<-errc
	if err != nil && !errors.Is(err, net.ErrClosed) {
		r.fail(tunnel, err)
	}
}

// fail records why a tunnel broke, in the log and in /_twin/state.
func (r *Router) fail(t *Tunnel, err error) {
	slog.Error("connect failed", "host", t.Host, "target", t.Target, "err", err)
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Error = err.Error()
}

// Serve runs the router on addr. It blocks.
func (r *Router) Serve(addr string) error {
	slog.Info("router listening", "addr", addr, "routes", r.routes)
	srv := &http.Server{Addr: addr, Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}
