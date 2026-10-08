package sidecartui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
)

// probeTimeout bounds one loopback dial. A local port answers in well under
// a millisecond or refuses at once; the timeout only matters for a firewall
// that drops instead of refusing.
const probeTimeout = 150 * time.Millisecond

// probes are the default ports a database listens on, in the order a tie is
// broken: the first open one becomes the starter's listener.
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

// Detect infers the starter config's input from this machine: the backend a
// URL in the environment names, the database ports open on loopback, and
// which model provider has a credential set.
//
// The environment wins over a probe: DATABASE_URL is a statement of intent,
// an open port is a guess. Nothing found falls back to Postgres on its
// default port, labelled as a default so the file says it was not seen.
// Detect reads environment variable NAMES only. It never reads a key's value.
func Detect(ctx context.Context, getenv func(string) string) configyaml.StarterInput {
	var found []configyaml.Upstream
	if u, ok := fromDatabaseURL(getenv("DATABASE_URL")); ok {
		found = append(found, u)
	}
	if h, p := getenv("PGHOST"), getenv("PGPORT"); h != "" || p != "" {
		if h == "" || h[0] == '/' {
			h = "127.0.0.1" // a socket directory: the TCP port is still on loopback
		}
		if p == "" {
			p = "5432"
		}
		found = append(found, configyaml.Upstream{Protocol: "postgres", Addr: net.JoinHostPort(h, p), Source: "PGHOST/PGPORT"})
	}
	found = append(found, probeLoopback(ctx)...)

	in := configyaml.StarterInput{AnalyzerProvider: analyzerProvider(getenv)}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		in.KeyDir = filepath.Join(home, ".hoop", "sidecar")
	}
	seen := map[string]bool{}
	for _, u := range found {
		if seen[u.Addr] {
			continue
		}
		seen[u.Addr] = true
		if in.Primary.Protocol == "" {
			in.Primary = u
			continue
		}
		in.Others = append(in.Others, u)
	}
	if in.Primary.Protocol == "" {
		in.Primary = configyaml.Upstream{Protocol: "postgres", Addr: "127.0.0.1:5432",
			Source: "default, nothing was found listening; edit upstream to point at your database"}
	}
	return in
}

// fromDatabaseURL reads the protocol and host:port out of a connection URL.
// The credentials in it are not looked at: the starter has no use for them.
func fromDatabaseURL(raw string) (configyaml.Upstream, bool) {
	if raw == "" {
		return configyaml.Upstream{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return configyaml.Upstream{}, false
	}
	s, ok := urlSchemes[u.Scheme]
	if !ok || !configyaml.StarterProtocol(s.protocol) {
		return configyaml.Upstream{}, false
	}
	port := u.Port()
	if port == "" {
		port = s.port
	}
	return configyaml.Upstream{Protocol: s.protocol, Addr: net.JoinHostPort(u.Hostname(), port), Source: "DATABASE_URL"}, true
}

// probeLoopback dials every default port at once and returns the open ones
// in probe order. A dial that is refused or times out is a port nobody holds.
func probeLoopback(ctx context.Context) []configyaml.Upstream {
	open := make([]bool, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Go(func() {
			open[i] = dialOpen(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port)))
		})
	}
	wg.Wait()
	var out []configyaml.Upstream
	for i, p := range probes {
		if open[i] {
			out = append(out, configyaml.Upstream{Protocol: p.protocol,
				Addr:   net.JoinHostPort("127.0.0.1", strconv.Itoa(p.port)),
				Source: fmt.Sprintf("port %d is open on this machine", p.port)})
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

// analyzerProvider names the provider whose credential is set, checking
// presence only.
func analyzerProvider(getenv func(string) string) string {
	switch {
	case getenv("ANTHROPIC_API_KEY") != "":
		return "anthropic"
	case getenv("OPENAI_API_KEY") != "":
		return "openai"
	case getenv("GOOGLE_APPLICATION_CREDENTIALS") != "":
		return "vertex"
	}
	return ""
}

// StarterResult is what pressing w produced, for the screen.
type StarterResult struct {
	Input configyaml.StarterInput
	// Path is the file written, "" when nothing was.
	Path string
	// Err is why nothing was written.
	Err error
	// Summary is the validator's one line, and ValidateErr its refusal.
	Summary     string
	ValidateErr error
}

// errStarterExists names the file a second w would have overwritten.
var errStarterExists = errors.New("already exists; nothing was overwritten")

// WriteStarter detects, renders and writes the starter config into dir, then
// validates what it wrote. An existing file is never replaced: the person
// may have edited it.
func WriteStarter(ctx context.Context, dir string, getenv func(string) string,
	validate func(path string) (string, error)) StarterResult {
	in := Detect(ctx, getenv)
	res := StarterResult{Input: in}
	body, err := configyaml.Starter(in)
	if err != nil {
		res.Err = err
		return res
	}
	path := configyaml.StarterFile
	if dir != "" {
		path = dir + string(os.PathSeparator) + configyaml.StarterFile
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			err = fmt.Errorf("%s %w", path, errStarterExists)
		}
		res.Err = err
		return res
	}
	_, werr := f.Write(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		res.Err = fmt.Errorf("writing %s: %w", path, werr)
		return res
	}
	res.Path = path
	if validate != nil {
		res.Summary, res.ValidateErr = validate(path)
	} else {
		res.ValidateErr = errors.New("this build has no validator")
	}
	return res
}
