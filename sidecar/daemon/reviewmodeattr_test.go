package daemon

import (
	"encoding/binary"
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	codecmysql "github.com/hoophq/hoop/sidecar/codec/mysql"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// mysqlDeleteFrom decodes DELETE FROM users on a MySQL connection whose
// handshake sent attrs, through the codec every MySQL lane gets.
func mysqlDeleteFrom(t *testing.T, attrs ...string) inspect.Statement {
	t.Helper()
	pkt := func(seq byte, p []byte) []byte {
		return append([]byte{byte(len(p)), byte(len(p) >> 8), byte(len(p) >> 16), seq}, p...)
	}
	lenenc := func(dst []byte, s string) []byte { return append(append(dst, byte(len(s))), s...) }

	hs := binary.LittleEndian.AppendUint32(nil, 1<<9|1<<20) // PROTOCOL_41, CONNECT_ATTRS
	hs = append(hs, make([]byte, 28)...)
	hs = append(hs, "app\x00secret\x00"...)
	var pairs []byte
	for _, s := range attrs {
		pairs = lenenc(pairs, s)
	}
	hs = lenenc(hs, string(pairs))

	c := codecmysql.New()
	var stmts []inspect.Statement
	for _, step := range []struct {
		dir inspect.Direction
		b   []byte
	}{
		{inspect.FromServer, pkt(0, append([]byte{10}, make([]byte, 40)...))},
		{inspect.FromClient, pkt(1, hs)},
		{inspect.FromServer, pkt(2, []byte{0, 0, 0, 2, 0, 0, 0})},
		{inspect.FromClient, pkt(0, []byte("\x03DELETE FROM users"))},
	} {
		got, n, err := c.Decode(step.dir, step.b)
		if err != nil || n != len(step.b) {
			t.Fatalf("decode: n=%d of %d, err=%v", n, len(step.b), err)
		}
		stmts = append(stmts, got...)
	}
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1", len(stmts))
	}
	return stmts[0]
}

// The ticket's done-when, from MySQL wire bytes through a real lane policy:
// on a hold lane, a connection with the attribute resolves to return, and
// one without it to the listener's hold.
func TestAHoldLaneReturnsAMySQLConnectionThatAsksForIt(t *testing.T) {
	for name, tc := range map[string]struct {
		attrs        []string
		mode, source string
	}{
		"with the attribute":    {[]string{analyzer.ConnectAttrReviewMode, "return"}, "return", "client"},
		"without the attribute": {[]string{"_client_name", "libmysql"}, "hold", "listener"},
	} {
		t.Run(name, func(t *testing.T) {
			// REJECTED settles the review at filing, so a hold ends without
			// waiting out the budget. That hold waits is analyzer's test.
			cp, calls := reviewPlane(t, http.StatusCreated,
				`{"forward":false,"review":{"id":"9f97","status":"REJECTED"}}`)
			deps := &analyzerDeps{
				cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
				provider: highRiskProvider{},
				cp:       cp,
			}
			pol, err := buildPolicy("appdb", GuardrailsConfig{}, holdingBlock(), nil, nil, deps)
			if err != nil {
				t.Fatalf("buildPolicy: %v", err)
			}
			v := pol.Evaluate(mysqlDeleteFrom(t, tc.attrs...))

			if !v.Denied || !strings.Contains(v.Message, "9f97") {
				t.Fatalf("denied=%v message=%q, want a denial naming the review", v.Denied, v.Message)
			}
			if got := v.Annotations[analyzer.MetadataReviewMode]; got != tc.mode {
				t.Errorf("review_mode is %q, want %s", got, tc.mode)
			}
			if got := v.Annotations[analyzer.MetadataReviewModeSource]; got != tc.source {
				t.Errorf("review_mode_source is %q, want %s", got, tc.source)
			}
			if len(*calls) != 1 {
				t.Errorf("the plane saw %d requests, want 1 filing", len(*calls))
			}
		})
	}
}
