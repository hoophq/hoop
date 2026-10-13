// Package router is the HTTPS_PROXY a scenario points the code under test
// at (reachability option B). It answers CONNECT by the requested hostname:
// a mapped host is tunnelled to its twin, any other host is refused and
// recorded. TLS stays end to end between the client and the twin, which
// presents a test-CA certificate for the real hostname.
package router

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/twins/kit"
)

type Tunnel struct {
	Host    string    `json:"host"`
	Target  string    `json:"target,omitempty"`
	Allowed bool      `json:"allowed"`
	At      time.Time `json:"at"`
}

type Router struct {
	mu      sync.Mutex
	routes  map[string]string // hostname -> host:port of the twin
	tunnels []Tunnel
}

// New takes host=target pairs: "slack.com=slack-twin:443".
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
	return map[string]any{"routes": r.routes, "tunnels": r.tunnels}
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
	r.tunnels = append(r.tunnels, Tunnel{Host: req.Host, Target: target, Allowed: ok, At: time.Now().UTC()})
	r.mu.Unlock()
	if !ok {
		log.Printf("[router] REFUSED CONNECT %s: no twin for this host", req.Host)
		http.Error(w, "no twin for "+host, http.StatusForbidden)
		return
	}
	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		log.Printf("[router] CONNECT %s -> %s: %v", req.Host, target, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n")
	log.Printf("[router] CONNECT %s -> %s", req.Host, target)
	go func() {
		// The client may have sent its TLS hello along with CONNECT.
		if n := buf.Reader.Buffered(); n > 0 {
			b, _ := buf.Reader.Peek(n)
			upstream.Write(b)
		}
		io.Copy(upstream, client)
		upstream.Close()
	}()
	io.Copy(client, upstream)
	client.Close()
}

// Serve runs the router on addr. It blocks.
func (r *Router) Serve(addr string) error {
	log.Printf("[router] proxy and admin on %s, routes %v", addr, r.routes)
	srv := &http.Server{Addr: addr, Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}
