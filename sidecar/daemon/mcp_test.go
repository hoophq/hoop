package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// statusPlane answers every request with one canned response and records the
// method and path, so a test can prove the status read never claims.
func statusPlane(t *testing.T, status int, body string) (*controlPlane, *[]string) {
	t.Helper()
	calls := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path+" "+r.Header.Get(sidecarTokenHeader))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &controlPlane{url: srv.URL, token: "hsc_token"}, calls
}

func TestAStatusReadIsOneGetAndNeverAClaim(t *testing.T) {
	cp, calls := statusPlane(t, http.StatusOK, `{"id":"9f97c0de-0000-4000-8000-000000000001","status":"APPROVED",`+
		`"listener_name":"appdb","approval_rule":"dba","created_at":"2026-09-28T10:00:00Z",`+
		`"decided_at":"2026-09-28T10:05:00Z"}`)
	// Upper case is the same id: the plane answers in lower case.
	got, err := cp.ReviewStatus(context.Background(), "9F97C0DE-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "APPROVED" || got.ListenerName != "appdb" || got.ApprovalRule != "dba" ||
		got.DecidedAt == nil {
		t.Errorf("status decoded as %+v", got)
	}
	if want := "GET /api/sidecars/reviews/9f97c0de-0000-4000-8000-000000000001 hsc_token"; len(*calls) != 1 || (*calls)[0] != want {
		t.Errorf("plane saw %q, want exactly %q", *calls, want)
	}
}

// An old plane has no status route and answers Gin's plain-text 404. Reading
// that as "not found" would tell an agent to abandon a review a human can
// still approve.
func TestAStatus404TellsAnOldPlaneFromAMissingReview(t *testing.T) {
	cp, _ := statusPlane(t, http.StatusNotFound, `{"message":"review not found"}`)
	if _, err := cp.ReviewStatus(context.Background(), testReviewID); !errors.Is(err, ErrReviewNotFound) {
		t.Errorf("the route's own 404 read as %v", err)
	}
	cp, _ = statusPlane(t, http.StatusNotFound, `404 page not found`)
	if _, err := cp.ReviewStatus(context.Background(), testReviewID); !errors.Is(err, ErrPlaneTooOld) {
		t.Errorf("an old plane's 404 read as %v", err)
	}
}

func TestEveryStatusRefusalIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		want string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"message":"access denied"}`, "rejected the token"},
		{"not a control plane", http.StatusPreconditionFailed, `{"message":"no"}`, "does not serve sidecar reviews"},
		{"another review", http.StatusOK, `{"id":"other","status":"PENDING"}`, "when asked about"},
		{"not json", http.StatusOK, `<html>`, "could not be read"},
		{"server error", http.StatusInternalServerError, `{"message":"boom"}`, "500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, _ := statusPlane(t, tc.code, tc.body)
			_, err := cp.ReviewStatus(context.Background(), testReviewID)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

const testReviewID = "9f97c0de-0000-4000-8000-000000000001"

// An id the plane could not have issued never reaches it. Sent, ".." or
// "a/b" would miss the route, and Gin's plain 404 would read as an old plane.
func TestAMalformedIDIsNotFoundWithoutAskingThePlane(t *testing.T) {
	cp, calls := statusPlane(t, http.StatusOK, `{}`)
	for _, id := range []string{"", "..", ".", "a/b", "r1", testReviewID + "/claim",
		"9f97c0de00004000800000000000000001", "{" + testReviewID + "}"} {
		if _, err := cp.ReviewStatus(context.Background(), id); !errors.Is(err, ErrReviewNotFound) {
			t.Errorf("id %q read as %v", id, err)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("malformed ids reached the plane: %q", *calls)
	}
}

func TestAnMCPBlockNeedsAValidListenAddress(t *testing.T) {
	for _, tc := range []struct {
		listen string
		want   string
	}{
		{"", `"listen" is required`},
		{"8765", "missing port"},
		{"127.0.0.1:notaport", "unknown port"},
		{"127.0.0.1:70000", "invalid port"},
	} {
		problems := (&MCPConfig{Listen: tc.listen}).validate()
		if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
			t.Errorf("listen %q: problems %q, want one containing %q", tc.listen, problems, tc.want)
		}
	}
	if p := (&MCPConfig{Listen: "127.0.0.1:8765"}).validate(); p != nil {
		t.Errorf("a valid address was refused: %q", p)
	}
	if p := (*MCPConfig)(nil).validate(); p != nil {
		t.Errorf("an absent block was refused: %q", p)
	}
}

// withMCPServer swaps the registry for one test. The registry is global and
// set from init in real binaries, so a test must put it back.
func withMCPServer(t *testing.T, serve MCPServe) {
	t.Helper()
	mcpMu.Lock()
	prev := mcpServe
	mcpServe = serve
	mcpMu.Unlock()
	t.Cleanup(func() {
		mcpMu.Lock()
		mcpServe = prev
		mcpMu.Unlock()
	})
}

func TestAnMCPBlockIsRefusedWhereItCannotServe(t *testing.T) {
	t.Setenv(ControlPlaneURLEnv, "")
	cfg := &Config{MCP: &MCPConfig{Listen: "127.0.0.1:8765"}}

	withMCPServer(t, nil)
	if err := checkMCP(cfg); err == nil || !strings.Contains(err.Error(), "no MCP server") {
		t.Errorf("a build with no MCP server accepted the block: %v", err)
	}

	withMCPServer(t, func(context.Context, string, ReviewStatusReader, *slog.Logger) error { return nil })
	if err := checkMCP(cfg); err == nil || !strings.Contains(err.Error(), "no control plane") {
		t.Errorf("a process with no plane accepted the block: %v", err)
	}

	// -validate against a file that names a plane: no connection yet.
	cfg.ControlPlaneURL = "https://cp.example.com"
	if err := checkMCP(cfg); err != nil {
		t.Errorf("a file naming a plane was refused: %v", err)
	}

	// A running process holds the connection; its document carries no URL.
	cfg.ControlPlaneURL = ""
	cfg.cp = &controlPlane{url: "https://cp.example.com", token: "t"}
	if err := checkMCP(cfg); err != nil {
		t.Errorf("a plane-connected process was refused: %v", err)
	}

	if err := checkMCP(&Config{}); err != nil {
		t.Errorf("a config with no mcp block was refused: %v", err)
	}
}

func TestRegisteringTwoMCPServersPanics(t *testing.T) {
	serve := func(context.Context, string, ReviewStatusReader, *slog.Logger) error { return nil }
	withMCPServer(t, serve)
	defer func() {
		if recover() == nil {
			t.Error("a second registration was accepted")
		}
	}()
	RegisterMCPServer(serve)
}
