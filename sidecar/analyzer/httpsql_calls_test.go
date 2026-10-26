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
// call, and that call carries the SQL, not the JSON around it.
func TestHTTPSQLRequestIsOneModelCall(t *testing.T) {
	c := codechttp.New(codechttp.Options{SQL: &codechttp.SQLOptions{
		Dialect: lexer.Spark, DialectName: "spark",
		Fields:        map[string]string{"/api/2.0/sql/statements": "statement"},
		MatchResource: policy.MatchResource,
	}})
	body := `{"statement":"DELETE FROM customers WHERE id = 7","warehouse_id":"w"}`
	raw := fmt.Sprintf("POST /api/2.0/sql/statements HTTP/1.1\r\nHost: h\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	stmts, _, err := c.Decode(inspect.FromClient, []byte(raw))
	if err != nil || len(stmts) != 2 {
		t.Fatalf("Decode: %d statements, %v", len(stmts), err)
	}

	p := &recordingProvider{}
	ev := mustNew(t, analyzer.Config{
		Provider: p,
		Trigger:  analyzer.Trigger{All: true},
		Actions:  analyzer.ActionMap{analyzer.RiskHigh: analyzer.ActionBlock},
	})
	for _, st := range stmts {
		ev.Evaluate(st)
	}
	if len(p.prompts) != 1 {
		t.Fatalf("the model was called %d times for one request: %q", len(p.prompts), p.prompts)
	}
	if !strings.Contains(p.prompts[0], "DELETE FROM customers") || strings.Contains(p.prompts[0], "warehouse_id") {
		t.Errorf("the call does not carry the SQL alone: %q", p.prompts[0])
	}
}
