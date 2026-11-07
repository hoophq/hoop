//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// A batch must not wait on a session another request holds, often the
// abandoned request of the same batch. The plane answers 503 and applies
// nothing of that session; the resend applies it, and dedups the rest.
func TestSidecarEventsAnswer503WhileASessionIsHeld(t *testing.T) {
	if testGateway.Pglite != nil {
		t.Skip("PGlite runs one backend: its advisory locks never contend")
	}
	orgID := uuid.NewString()
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, ?)`,
		orgID, "sidecar-events-lock-"+orgID).Error; err != nil {
		t.Fatalf("create org: %v", err)
	}
	featureflag.Set(orgID, services.SidecarSessionEventsFlag, true)
	t.Cleanup(func() { featureflag.SetAll(orgID, map[string]bool{}) })
	const token = "hsc_events_lock_test"
	sc := &models.Sidecar{OrgID: orgID, Name: "edge", KeyHash: models.HashAPIKey(token), CreatedBy: "tests@hoop.dev"}
	if err := models.CreateSidecar(models.DB, sc); err != nil {
		t.Fatalf("create sidecar: %v", err)
	}

	t0 := time.Now().UTC().Truncate(time.Second)
	event := func(seq int64, sid string, kind audit.Kind, statement string) daemon.SessionEvent {
		return daemon.SessionEvent{Seq: seq, Event: audit.Event{
			Kind: kind, Timestamp: t0.Add(time.Duration(seq) * time.Second), SessionID: session.ID(sid),
			Principal: "alice@example.com", Protocol: inspect.Postgres, Connection: "appdb",
			Statement: statement, Allowed: statement != "",
		}}
	}
	body, err := json.Marshal(daemon.SessionEventsRequest{Events: []daemon.SessionEvent{
		event(1, "s-free", audit.KindSessionStart, ""),
		event(2, "s-free", audit.KindStatement, "SELECT 1"),
		event(1, "s-held", audit.KindSessionStart, ""),
		event(2, "s-held", audit.KindStatement, "SELECT 2"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	post := func() (int, []byte) {
		req, err := http.NewRequest(http.MethodPost, testServer.BaseURL+"/api/sidecars/events", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(apiroutes.SidecarTokenHeader, token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post events: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, []byte(testutil.ReadBody(t, resp))
	}
	sessions := func(sid string) int64 {
		var n int64
		if err := models.DB.Raw(`SELECT count(*) FROM private.sessions WHERE org_id = ? AND id = ?`,
			orgID, services.SidecarSessionID(sc.ID, sid)).Scan(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Another request writes s-held.
	holder := models.DB.Begin()
	if holder.Error != nil {
		t.Fatal(holder.Error)
	}
	t.Cleanup(func() { holder.Rollback() })
	if err := models.LockSidecarSession(holder, services.SidecarSessionID(sc.ID, "s-held")); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	status, raw := post()
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status %d while the session is held, want 503 (body: %s)", status, raw)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("the request waited %v on the held session", waited)
	}
	if n := sessions("s-held"); n != 0 {
		t.Errorf("the held session has %d rows, want none", n)
	}
	if n := sessions("s-free"); n != 1 {
		t.Errorf("the free session of the batch has %d rows, want it applied", n)
	}

	if err := holder.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	status, raw = post()
	if status != http.StatusOK {
		t.Fatalf("status %d on the resend, want 200 (body: %s)", status, raw)
	}
	var got openapi.SidecarSessionEventsResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Accepted != 2 || got.Duplicates != 2 {
		t.Errorf("the resend accepted %d and skipped %d, want the held session applied and the free one skipped",
			got.Accepted, got.Duplicates)
	}
	if n := sessions("s-held"); n != 1 {
		t.Errorf("the held session has %d rows after the resend, want 1", n)
	}
}
