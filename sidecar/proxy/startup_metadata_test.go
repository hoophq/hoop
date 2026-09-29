package proxy_test

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// pgStartupWith builds a v3 StartupMessage from key/value pairs.
func pgStartupWith(kv ...string) []byte {
	var params []byte
	for _, s := range kv {
		params = append(params, s...)
		params = append(params, 0)
	}
	params = append(params, 0)
	out := make([]byte, 8, 8+len(params))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(params)))
	binary.BigEndian.PutUint32(out[4:8], 3<<16)
	return append(out, params...)
}

// A client that sets PGOPTIONS='-c claude.session.id=...' gets that id on
// every statement it runs and on the session's closing record, and the
// startup packet still reaches the upstream byte for byte: the backend has to
// see the same options, or the setting the database reads and the one the
// trail records could differ.
func TestStartupOptionReachesTheTrail(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)
	srv := startServer(t, proxy.Config{
		Upstream:   up.addr(),
		Protocol:   inspect.Postgres,
		Connection: "appdb",
		Audit:      sink,
		StartupMetadata: []proxy.StartupMetadata{
			{Option: "claude.session.id", Key: "claude.session.id"},
		},
	})

	startup := pgStartupWith(
		"user", "alice", "database", "appdb",
		"options", "-c claude.session.id=xyz1234678")
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write(startup); err != nil {
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

	var stmt, end *audit.Event
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (stmt == nil || end == nil) {
		for _, ev := range sink.Events() {
			switch {
			case ev.Kind == audit.KindStatement && strings.Contains(ev.Statement, "SELECT 1"):
				stmt = &ev
			case ev.Kind == audit.KindSessionEnd:
				end = &ev
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stmt == nil || end == nil {
		t.Fatalf("statement=%v session_end=%v; want both recorded", stmt != nil, end != nil)
	}
	for _, ev := range []*audit.Event{stmt, end} {
		if got := ev.Metadata["claude.session.id"]; got != "xyz1234678" {
			t.Errorf("%s metadata[claude.session.id] = %q, want xyz1234678", ev.Kind, got)
		}
		if ev.Principal != "alice" {
			t.Errorf("%s principal = %q, want alice", ev.Kind, ev.Principal)
		}
	}
	if !strings.HasPrefix(string(up.got()), string(startup)) {
		t.Error("the upstream did not receive the startup packet intact")
	}
}

// Startup metadata is read from a pgwire StartupMessage. On another lane it
// would load and record nothing, and a key the policy context owns would let
// the client name its own principal; both are refused at construction.
func TestStartupMetadataIsRefusedWhereItCannotApply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proto inspect.Protocol
		field proxy.StartupMetadata
		want  string
	}{
		{"not postgres", inspect.MySQL,
			proxy.StartupMetadata{Option: "claude.session.id", Key: "claude.session.id"}, "not a mysql lane"},
		{"reserved key", inspect.Postgres,
			proxy.StartupMetadata{Option: "claude.user", Key: "principal"}, "reserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := proxy.NewServer(proxy.Config{
				Listen:          "127.0.0.1:0",
				Upstream:        "127.0.0.1:1",
				Protocol:        tc.proto,
				StartupMetadata: []proxy.StartupMetadata{tc.field},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewServer = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}
