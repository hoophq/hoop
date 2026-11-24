package gate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/hoophq/hoop/sidecar/telemetry"
)

// countingLaneLog is a LaneRecorder that is safe for concurrent use, as a
// lane's recorder must be.
type countingLaneLog struct {
	mu                 sync.Mutex
	statements, stages int
}

func (l *countingLaneLog) BeginExchange(ctx context.Context, _ telemetry.ExchangeKind, _ ...telemetry.Attr) (context.Context, telemetry.EndExchange) {
	return ctx, func(error) {}
}

func (l *countingLaneLog) BeginStatement(ctx context.Context, _ inspect.Statement) (context.Context, telemetry.EndStatement) {
	return ctx, func(telemetry.Verdict) {
		l.mu.Lock()
		l.statements++
		l.mu.Unlock()
	}
}

func (l *countingLaneLog) BeginStage(ctx context.Context, _ telemetry.Stage, _ string) (context.Context, telemetry.EndStage) {
	return ctx, func(string, error) {
		l.mu.Lock()
		l.stages++
		l.mu.Unlock()
	}
}

func (l *countingLaneLog) Upstream(context.Context, time.Duration, telemetry.Pairing) {}
func (l *countingLaneLog) Masked(int)                                                 {}
func (l *countingLaneLog) AuditError()                                                {}
func (l *countingLaneLog) MCPMessage(telemetry.MCPMessage)                            {}

// barrierOPA allows everything. Its first n decisions answer only once all n
// are in flight, so a gate that judged one statement at a time never gets
// past them. A request statement ending in an even number opts out of OPA
// on the responses.
func barrierOPA(t *testing.T, n int) string {
	t.Helper()
	var (
		mu      sync.Mutex
		arrived int
		all     = make(chan struct{})
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input struct {
				Direction string `json:"direction"`
				Statement string `json:"statement"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		mu.Lock()
		if arrived++; arrived == n {
			close(all)
		}
		mu.Unlock()
		select {
		case <-all:
		case <-time.After(5 * time.Second):
			t.Errorf("%q waited for %d decisions in flight; the gate judged one at a time", body.Input.Statement, n)
		}
		result := `{"result":{"allow":true}}`
		if i, err := strconv.Atoi(body.Input.Statement[strings.LastIndex(body.Input.Statement, " ")+1:]); err == nil &&
			body.Input.Direction == string(inspect.FromClient) && i%2 == 0 {
			result = `{"result":{"allow":true,"responses":false}}`
		}
		_, _ = w.Write([]byte(result))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// EvaluateStatement and RecordActivity run concurrently on one statement
// gate, the mcp lane's use, with local rules, OPA and telemetry set. Run
// with -race.
func TestAStatementGateJudgesAndRecordsConcurrently(t *testing.T) {
	const n = 8
	rules, err := policy.NewRules([]policy.Rule{{Name: "no-drops", Type: policy.MatchDenyWords, Words: []string{"DROP"}, Message: "no"}})
	if err != nil {
		t.Fatal(err)
	}
	tel := &countingLaneLog{}
	sink := audit.NewMemorySink(1024)
	g, err := gate.NewStatementGate(
		session.New(inspect.MCP, session.Identity{Subject: "alice"}),
		gate.Config{
			Protocol:  inspect.MCP,
			Policy:    policy.Chain{rules, &policy.OPAClient{URL: barrierOPA(t, n), Timeout: 10 * time.Second}},
			Audit:     sink,
			Telemetry: tel,
			Settle:    func(context.Context, string, bool) {},
		})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stmt := func(dir inspect.Direction, text string) inspect.Statement {
		return inspect.Statement{Protocol: inspect.MCP, Direction: dir, Text: text, Operation: inspect.OpCall}
	}

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := strconv.Itoa(i)
			if err := g.RecordActivity(ctx, "before", map[string]string{"i": id}); err != nil {
				t.Error(err)
			}
			if d := g.EvaluateStatement(ctx, stmt(inspect.FromClient, "call "+id)); !d.Allowed {
				t.Errorf("call %s: %+v", id, d)
			}
			if d := g.EvaluateStatement(ctx, stmt(inspect.FromServer, "result "+id)); !d.Allowed {
				t.Errorf("result %s: %+v", id, d)
			}
			if d := g.EvaluateStatement(ctx, stmt(inspect.FromClient, "DROP "+id)); d.Allowed {
				t.Errorf("DROP %s was allowed", id)
			}
			if err := g.RecordActivity(ctx, "after", map[string]string{"i": id}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := g.Close(ctx); err != nil {
		t.Fatal(err)
	}

	if statements, denied := g.Stats(); statements != 3*n || denied != n {
		t.Errorf("stats %d statements, %d denied; want %d and %d", statements, denied, 3*n, n)
	}
	tel.mu.Lock()
	if tel.statements != 3*n || tel.stages == 0 {
		t.Errorf("telemetry saw %d statements and %d stages, want %d and some", tel.statements, tel.stages, 3*n)
	}
	tel.mu.Unlock()
	kinds := map[audit.Kind]int{}
	events := sink.Events()
	for _, e := range events {
		kinds[e.Kind]++
	}
	if kinds[audit.KindActivity] != 2*n || kinds[audit.KindStatement] != 2*n || kinds[audit.KindViolation] != n ||
		events[0].Kind != audit.KindSessionStart || events[len(events)-1].Kind != audit.KindSessionEnd {
		t.Errorf("trail %v", kinds)
	}
}
