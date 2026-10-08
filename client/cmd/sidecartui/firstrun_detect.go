package sidecartui

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// probeTimeout bounds one loopback dial. A local port answers in well under
// a millisecond or refuses at once; the timeout only matters for a firewall
// that drops instead of refusing.
const probeTimeout = 150 * time.Millisecond

// probes are the default ports a backend listens on, in the order a tie is
// broken: the first one found is the protocol the setup list puts its
// cursor on.
var probes = []struct {
	protocol string
	port     int
}{
	{"postgres", 5432},
	{"mysql", 3306},
	{"mssql", 1433},
	{"mongodb", 27017},
	{"oracle", 1521},
	{"clickhouse", 8123},
}

// urlSchemes maps a DATABASE_URL scheme to the protocol and the port it
// implies when the URL names none.
var urlSchemes = map[string]struct {
	protocol string
	port     string
}{
	"postgres":   {"postgres", "5432"},
	"postgresql": {"postgres", "5432"},
	"mysql":      {"mysql", "3306"},
	"sqlserver":  {"mssql", "1433"},
	"mssql":      {"mssql", "1433"},
	"mongodb":    {"mongodb", "27017"},
	"clickhouse": {"clickhouse", "8123"},
}

// found is one backend seen on this machine, and how it was seen, so the
// setup screen can say why it suggests it.
type found struct {
	protocol string
	addr     string
	source   string
}

// machine is what the setup screen learns from this machine before it asks
// anything.
type machine struct {
	// found lists backends, the environment's first, then open ports.
	found []found
	// provider names the model provider whose credential is set, "" for
	// none. Read from variable NAMES only: a key's value is never read.
	provider string
	// keyDir is where the analyzer's key file is suggested, absolute
	// because credentials_file is read as written and "~" is not expanded.
	keyDir string
}

// foundFor returns the first backend seen for protocol.
func (m machine) foundFor(protocol string) (found, bool) {
	for _, f := range m.found {
		if f.protocol == protocol {
			return f, true
		}
	}
	return found{}, false
}

// detect reads the environment and dials the default ports on loopback.
// The environment comes first: DATABASE_URL is a statement of intent, an
// open port is a guess.
func detect(ctx context.Context, getenv func(string) string) machine {
	var m machine
	if f, ok := fromDatabaseURL(getenv("DATABASE_URL")); ok {
		m.found = append(m.found, f)
	}
	if h, p := getenv("PGHOST"), getenv("PGPORT"); h != "" || p != "" {
		if h == "" || h[0] == '/' {
			h = "127.0.0.1" // a socket directory: the TCP port is still on loopback
		}
		if p == "" {
			p = "5432"
		}
		m.found = append(m.found, found{"postgres", net.JoinHostPort(h, p), "PGHOST/PGPORT"})
	}
	seen := map[string]bool{}
	for _, f := range m.found {
		seen[f.addr] = true
	}
	for _, f := range probeLoopback(ctx) {
		if !seen[f.addr] {
			m.found = append(m.found, f)
		}
	}
	switch {
	case getenv("ANTHROPIC_API_KEY") != "":
		m.provider = "anthropic"
	case getenv("OPENAI_API_KEY") != "":
		m.provider = "openai"
	case getenv("GOOGLE_APPLICATION_CREDENTIALS") != "":
		m.provider = "vertex"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		m.keyDir = filepath.Join(home, ".hoop", "sidecar")
	}
	return m
}

// fromDatabaseURL reads the protocol and host:port out of a connection URL.
// The credentials in it are not looked at.
func fromDatabaseURL(raw string) (found, bool) {
	if raw == "" {
		return found{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return found{}, false
	}
	s, ok := urlSchemes[u.Scheme]
	if !ok {
		return found{}, false
	}
	port := u.Port()
	if port == "" {
		port = s.port
	}
	return found{s.protocol, net.JoinHostPort(u.Hostname(), port), "DATABASE_URL"}, true
}

// probeLoopback dials every default port at once and returns the open ones
// in probe order.
func probeLoopback(ctx context.Context) []found {
	open := make([]bool, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Go(func() {
			open[i] = dialOpen(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port)))
		})
	}
	wg.Wait()
	var out []found
	for i, p := range probes {
		if open[i] {
			out = append(out, found{p.protocol, net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port)),
				fmt.Sprintf("port %d is open on this machine", p.port)})
		}
	}
	return out
}

func dialOpen(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
