package sidecardemo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Each demo has one AI step, held for approval and ticked only by the
// analyzer's high verdict, and it sits between the guardrail and the steps
// that pass: the order the tour teaches.
func TestEachDemoHasOneAIStep(t *testing.T) {
	for kind, steps := range map[Kind][]Step{KindHTTP: Steps, KindPostgres: PGSteps} {
		var ai []Step
		for _, s := range steps {
			if s.Approval {
				ai = append(ai, s)
			}
		}
		if len(ai) != 1 {
			t.Fatalf("%s: %d AI steps, want 1", kind, len(ai))
		}
		s := ai[0]
		if s.Direct || s.SeenRisk != "high" || s.SeenKind != "statement" {
			t.Errorf("%s: AI step %+v, want through the sidecar and ticked by a high verdict", kind, s)
		}
		if i := slices.IndexFunc(steps, func(x Step) bool { return x.Approval }); i != 2 || steps[1].SeenKind != "violation" {
			t.Errorf("%s: AI step at %d, want right after the guardrail", kind, i)
		}
		if !strings.Contains(s.Expect, "enter") {
			t.Errorf("%s: the step does not say how to decide: %q", kind, s.Expect)
		}
	}
	// The card columns make it high risk; the LIMIT keeps an approved run
	// from pulling the whole table through the sidecar into the tour.
	if !strings.Contains(PGSteps[2].SQL, "creditcard") || !strings.Contains(strings.ToUpper(PGSteps[2].SQL), "LIMIT") {
		t.Errorf("the Postgres AI step is not a bounded card read: %q", PGSteps[2].SQL)
	}
	if Steps[2].Method != "GET" || Steps[2].Path != "/users/export" {
		t.Errorf("the HTTP AI step is %s %s, want GET /users/export", Steps[2].Method, Steps[2].Path)
	}
}

// The guidance names what is high risk in each demo's own data, and the
// trigger sends only the reads to the model. The AI step is a read.
func TestDemoAnalyzerGuidanceAndTrigger(t *testing.T) {
	if p := AnalyzerPrompt(KindPostgres); !strings.Contains(p, "creditcard") || !strings.Contains(p, "HIGH") {
		t.Errorf("the Postgres guidance does not name the card columns: %q", p)
	}
	if p := AnalyzerPrompt(KindHTTP); !strings.Contains(p, "export") || !strings.Contains(p, "HIGH") {
		t.Errorf("the HTTP guidance does not name the export: %q", p)
	}
	if got := AnalyzerTrigger(KindPostgres); !slices.Equal(got, []string{"select"}) {
		t.Errorf("Postgres trigger = %v", got)
	}
	if got := AnalyzerTrigger(KindHTTP); !slices.Equal(got, []string{"get"}) {
		t.Errorf("HTTP trigger = %v", got)
	}
	// The scope reaches the AI step, and in HTTP nothing else: with the model
	// down the lane refuses what it scopes.
	if tables, _ := AnalyzerScope(KindPostgres); !slices.Equal(tables, []string{"customers"}) || !strings.Contains(PGSteps[2].SQL, "FROM customers") {
		t.Errorf("Postgres scope %v does not reach the AI step", tables)
	}
	if _, res := AnalyzerScope(KindHTTP); !slices.Equal(res, []string{Steps[2].Path}) {
		t.Errorf("HTTP scope %v, want only the AI step's path", res)
	}
	if AnalyzerTimeout >= analyzer.DefaultTimeout {
		t.Errorf("AnalyzerTimeout %v is not shorter than the analyzer default %v", AnalyzerTimeout, analyzer.DefaultTimeout)
	}
}

// The API waits for a held request's headers as long as the hold waits, or
// the approved request finds its connection closed.
func TestDemoAPIServesTheExportAndWaitsForAHold(t *testing.T) {
	if HeaderWait <= analyzer.ReviewWait {
		t.Errorf("HeaderWait %v does not outlast a hold (%v)", HeaderWait, analyzer.ReviewWait)
	}
	srv := httptest.NewServer(NewServer().Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/users/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var users []user
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(users) != 4 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("export = %d, %d users, %q", resp.StatusCode, len(users), resp.Header.Get("Content-Disposition"))
	}
}
