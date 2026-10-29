package analyzer_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// recordingProvider keeps every prompt it is sent and answers low.
type recordingProvider struct {
	mu      sync.Mutex
	prompts []string
}

func (p *recordingProvider) Name() string { return "recording" }

func (p *recordingProvider) Classify(_ context.Context, _, content string) (*analyzer.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prompts = append(p.prompts, content)
	return &analyzer.Result{RiskLevel: analyzer.RiskLow, Title: "t"}, nil
}

// A Statement Execution API request through an http.sql lane is one model
// call, and that call carries the SQL, not the JSON around it. A resource
// trigger on the route selects that SQL, whatever spelling of the path the
// client used; a trigger on another route selects nothing.
func TestHTTPSQLRequestIsOneModelCall(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  string
		trigger analyzer.Trigger
		calls   int
	}{
		{"every statement", "/api/2.0/sql/statements", analyzer.Trigger{All: true}, 1},
		{"resource trigger on the route", "/api/2.0/sql/statements",
			analyzer.Trigger{Resources: []string{"/api/2.0/sql/statements"}}, 1},
		{"resource trigger, dot segment", "/api/2.0/./sql/statements",
			analyzer.Trigger{Resources: []string{"/api/2.0/sql/**"}}, 1},
		{"resource trigger on another route", "/api/2.0/sql/statements",
			analyzer.Trigger{Resources: []string{"/api/2.0/jobs/**"}}, 0},
		{"method trigger", "/api/2.0/sql/statements",
			analyzer.Trigger{Operations: []inspect.Operation{inspect.OpPost}}, 1},
		{"SQL operation trigger", "/api/2.0/sql/statements",
			analyzer.Trigger{Operations: []inspect.Operation{inspect.OpDelete}}, 1},
		{"trigger on another method", "/api/2.0/sql/statements",
			analyzer.Trigger{Operations: []inspect.Operation{inspect.OpGet}}, 0},
		{"excluding the method keeps the SQL", "/api/2.0/sql/statements",
			analyzer.Trigger{All: true, Exclude: []analyzer.TriggerItem{{Operations: []inspect.Operation{inspect.OpPost}}}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := codechttp.New(codechttp.Options{SQL: &codechttp.SQLOptions{
				Dialect: lexer.Spark, DialectName: "spark",
				Fields:        map[string]string{"/api/2.0/sql/statements": "statement"},
				MatchResource: policy.MatchResource,
			}})
			body := `{"statement":"DELETE FROM customers WHERE id = 7","warehouse_id":"w"}`
			raw := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: h\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
				tc.target, len(body), body)
			stmts, _, err := c.Decode(inspect.FromClient, []byte(raw))
			if err != nil || len(stmts) != 2 {
				t.Fatalf("Decode: %d statements, %v", len(stmts), err)
			}

			p := &recordingProvider{}
			ev := mustNew(t, analyzer.Config{
				Provider: p,
				Trigger:  tc.trigger,
				Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
			})
			for _, st := range stmts {
				ev.Evaluate(st)
			}
			if len(p.prompts) != tc.calls {
				t.Fatalf("the model was called %d times, want %d: %q", len(p.prompts), tc.calls, p.prompts)
			}
			if tc.calls == 1 && (!strings.Contains(p.prompts[0], "DELETE FROM customers") || strings.Contains(p.prompts[0], "warehouse_id")) {
				t.Errorf("the call does not carry the SQL alone: %q", p.prompts[0])
			}
		})
	}
}
