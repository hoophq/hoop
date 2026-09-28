package proxy_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// syncBuffer collects log records written by the relay's goroutines while the
// test reads them.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// pgStartupUser builds a v3 StartupMessage naming user.
func pgStartupUser(user string) []byte {
	var params []byte
	for _, kv := range [][2]string{{"user", user}, {"database", "appdb"}} {
		params = append(params, kv[0]...)
		params = append(params, 0)
		params = append(params, kv[1]...)
		params = append(params, 0)
	}
	params = append(params, 0)

	out := make([]byte, 8, 8+len(params))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(params)))
	binary.BigEndian.PutUint32(out[4:8], 3<<16)
	return append(out, params...)
}

// topLevelValues returns every value recorded under key in one JSON log line.
// encoding/json into a map cannot answer this: a duplicate key silently wins
// over the earlier one, which is exactly the defect under test.
func topLevelValues(t *testing.T, line, key string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("log line is not a JSON object: %q", line)
	}
	var got []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("read value: %v", err)
		}
		if k == key {
			got = append(got, v.(string))
		}
	}
	return got
}

// A principal learned from the pgwire StartupMessage REPLACES the anonymous
// placeholder on the connection logger. slog.With appends, so extending the
// logger instead of rebuilding it wrote "principal" twice into every line of
// the session, and a JSON object with a repeated key is read differently by
// every consumer that ingests it.
func TestPostgresSessionLogNamesThePrincipalOnce(t *testing.T) {
	up := newEchoUpstream(t, nil)
	logs := &syncBuffer{}

	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Logger:     slog.New(slog.NewJSONHandler(logs, nil)),
	})

	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write(pgStartupUser("alice")); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if _, err := c.Write(pgQuery("SELECT 1")); err != nil {
		t.Fatalf("query: %v", err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 512)); err != nil {
		t.Fatalf("read: %v", err)
	}
	c.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logs.String(), "session closed") {
		time.Sleep(10 * time.Millisecond)
	}

	var checked int
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if !strings.Contains(line, `"session opened"`) && !strings.Contains(line, `"session closed"`) {
			continue
		}
		checked++
		got := topLevelValues(t, line, "principal")
		if len(got) != 1 || got[0] != "alice" {
			t.Errorf("principal keys = %q on %s; want exactly one, \"alice\"",
				got, line)
		}
	}
	if checked != 2 {
		t.Fatalf("saw %d session lifecycle log lines, want 2 (opened and closed):\n%s",
			checked, logs.String())
	}
}
