package proxy_test

import (
	"fmt"
	"strings"
	"testing"

	httpcodec "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// clickhouseLane is an http lane in front of ClickHouse's HTTP interface
// that reads the SQL in the query parameter and the body.
func clickhouseLane(t *testing.T, upstream string, pol policy.Evaluator) *proxy.Server {
	t.Helper()
	opts := httpcodec.Options{SQL: &httpcodec.SQLOptions{
		Dialect:       lexer.ClickHouse,
		DialectName:   "clickhouse",
		Params:        map[string]string{"/": "query"},
		MatchResource: policy.MatchResource,
	}}
	return startServer(t, proxy.Config{
		Upstream:     upstream,
		Protocol:     inspect.HTTP,
		Policy:       pol,
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: func() inspect.Codec { return httpcodec.New(opts) },
	})
}

func chRequest(method, target, headers, body string) string {
	return fmt.Sprintf("%s %s HTTP/1.1\r\nHost: h\r\n%sContent-Length: %d\r\n\r\n%s", method, target, headers, len(body), body)
}

// The SQL in ClickHouse's query parameter or body is judged before the
// request leaves. Writes and SQL the lane cannot read are answered with the
// rule's 403 and never reach the server; reads and an INSERT's binary rows
// go through untouched.
func TestHTTPSQLClickHouseIsJudgedBeforeItIsForwarded(t *testing.T) {
	ok := []byte("HTTP/1.1 200 OK\r\nContent-Type: text/tab-separated-values\r\nContent-Length: 4\r\n\r\nAda\n")
	for _, tc := range []struct {
		name, raw string
		denied    bool
	}{
		{"GET select", chRequest("GET", "/?query=SELECT%20name%20FROM%20appdb.customers", "", ""), false},
		{"POST drop in the parameter", chRequest("POST", "/?query=DROP%20TABLE%20appdb.customers", "", ""), true},
		{"POST delete in the body", chRequest("POST", "/", "", "DELETE FROM appdb.customers WHERE id = 1"), true},
		{"drop split across both", chRequest("POST", "/?query=DROP%20TABLE", "", "appdb.customers"), true},
		{"compressed body", chRequest("POST", "/", "Content-Encoding: gzip\r\n", "\x1f\x8b\x08\x00"), true},
		{"select in a body", chRequest("POST", "/?database=appdb", "", "SELECT 1 FORMAT TSV"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := newRouteUpstream(t, map[string][]byte{"/": ok})
			got := roundTrip(t, clickhouseLane(t, up.addr(), denyWritesAndUnknown(t)).Addr().String(), tc.raw)
			if tc.denied {
				if !strings.HasPrefix(got, "403") || !strings.Contains(got, "reads only") {
					t.Fatalf("not denied: %q", got)
				}
				if r := up.received(); len(r) != 0 {
					t.Fatalf("a denied request reached the upstream: %q", r)
				}
				return
			}
			if !strings.HasPrefix(got, "200") || len(up.received()) != 1 {
				t.Fatalf("a read was not relayed: %q, upstream %q", got, up.received())
			}
		})
	}

	// ClickHouse drops a fragment and runs the body alone, so a query
	// parameter after # must not stand in for the DROP in the body.
	up := newRouteUpstream(t, map[string][]byte{"/": ok})
	got := roundTrip(t, clickhouseLane(t, up.addr(), denyWritesAndUnknown(t)).Addr().String(),
		chRequest("POST", "/?max_threads=1#&query=SELECT%201", "", "DROP TABLE appdb.customers"))
	if !strings.HasPrefix(got, "403") || !strings.Contains(got, "#") || len(up.received()) != 0 {
		t.Fatalf("a fragment hid the DROP: %q, upstream %q", got, up.received())
	}

	// A lane that refuses drops only lets an INSERT through with its
	// binary rows as the client sent them.
	up = newRouteUpstream(t, map[string][]byte{"/": ok})
	rows := "\x01\x00\x00\x00\x00\x00\x00\x00\x03Ada\xff"
	got = roundTrip(t, clickhouseLane(t, up.addr(), denyDrops(t)).Addr().String(),
		chRequest("POST", "/?query=INSERT%20INTO%20appdb.customers%20FORMAT%20RowBinary", "", rows))
	if r := up.received(); !strings.HasPrefix(got, "200") || len(r) != 1 || !strings.HasSuffix(r[0], rows) {
		t.Fatalf("the INSERT was not relayed intact: %q, upstream %q", got, r)
	}
}
