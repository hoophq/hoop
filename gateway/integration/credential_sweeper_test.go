//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
)

// sweepBatch matches the batch size the credentialsweeper job uses per tick.
const sweepBatch = 500

type sweptSession struct {
	Status  string     `gorm:"column:status"`
	EndedAt *time.Time `gorm:"column:ended_at"`
}

func readSession(t *testing.T, sessionID string) sweptSession {
	t.Helper()
	var row sweptSession
	err := models.DB.Raw(`SELECT status, ended_at FROM private.sessions WHERE id = ?`, sessionID).
		Scan(&row).Error
	if err != nil {
		t.Fatalf("read session %s: %v", sessionID, err)
	}
	return row
}

func readCredentialSessionLink(t *testing.T, credentialID string) *string {
	t.Helper()
	var row struct {
		SessionID *string `gorm:"column:session_id"`
	}
	err := models.DB.Raw(`SELECT session_id FROM private.connection_credentials WHERE id = ?`, credentialID).
		Scan(&row).Error
	if err != nil {
		t.Fatalf("read credential %s: %v", credentialID, err)
	}
	return row.SessionID
}

// TestExpiredCredentialSessionsAreSweptOffTheReadPath covers the sweep
// contract from both sides.
//
// Closing the audit session of an expired credential used to be the first
// statement of GET /api/sessions and GET /api/sessions/{id}, so a read mutated,
// and a read scoped to one org closed sessions for every tenant on the
// deployment. The reads must now be pure: an expired credential's session
// stays exactly as it was, no matter how many times it is listed or fetched.
//
// The closing itself moved to models.CloseExpiredCredentialSessions, driven by
// the credentialsweeper job. It is exercised here directly (the ticker is not
// started by the test gateway) and must be idempotent: a second pass over the
// same state has nothing left to do.
func TestExpiredCredentialSessionsAreSweptOffTheReadPath(t *testing.T) {
	token := adminToken(t)
	const agentName = "cred-sweep-agent"
	agentID := createAgentReturningID(t, token, agentName)
	defer deleteAgent(t, token, agentName)

	const connName = "smoke-cred-sweep"
	created := testServer.Post(t, "/connections", token, openapi.Connection{
		Name:               connName,
		Type:               "database",
		SubType:            "postgres",
		AgentId:            agentID,
		Command:            []string{"psql"},
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "enabled",
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	defer func() {
		del := testServer.Delete(t, "/connections/"+connName, token)
		del.Body.Close()
	}()

	// A bounded credential (access_duration_seconds set) opens an audit
	// session the sweeper is responsible for closing. A persistent one is
	// born done and would never reach the sweeper.
	resp := testServer.Post(t, "/connections/"+connName+"/credentials", token,
		openapi.ConnectionCredentialsRequest{AccessDurationSec: 3600})
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusCreated)
	var minted map[string]any
	testutil.DecodeJSON(t, resp, &minted)

	credentialID, _ := minted["id"].(string)
	sessionID, _ := minted["session_id"].(string)
	if credentialID == "" || sessionID == "" {
		t.Fatalf("mint credential: expected an id and a session_id, got %v", minted)
	}
	if got := readSession(t, sessionID).Status; got != "open" {
		t.Fatalf("mint credential: expected a session in status open, got %q", got)
	}

	// Close the access window behind the API's back — waiting out a real one
	// would mean sleeping for the minimum grantable duration.
	err := models.DB.Table("private.connection_credentials").
		Where("id = ?", credentialID).
		Update("expire_at", time.Now().UTC().Add(-time.Hour)).Error
	if err != nil {
		t.Fatalf("expire credential: %v", err)
	}

	// The read paths must observe the expired credential without touching it.
	list := testServer.Get(t, "/sessions?limit=100", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)

	detail := testServer.Get(t, "/sessions/"+sessionID, token)
	defer detail.Body.Close()
	testutil.RequireStatus(t, detail, http.StatusOK)
	var fetched map[string]any
	testutil.DecodeJSON(t, detail, &fetched)
	if fetched["status"] != "open" {
		t.Errorf("GET /sessions/{id}: expected the untouched status open, got %v", fetched["status"])
	}

	afterReads := readSession(t, sessionID)
	if afterReads.Status != "open" {
		t.Errorf("read path mutated the session: status is %q, expected open", afterReads.Status)
	}
	if afterReads.EndedAt != nil {
		t.Errorf("read path mutated the session: ended_at is %v, expected NULL", afterReads.EndedAt)
	}
	if link := readCredentialSessionLink(t, credentialID); link == nil || *link != sessionID {
		t.Errorf("read path unlinked the credential: session_id is %v, expected %q", link, sessionID)
	}

	// The sweeper is what closes it.
	swept, err := models.CloseExpiredCredentialSessions(t.Context(), models.DB, sweepBatch)
	if err != nil {
		t.Fatalf("sweep expired credential sessions: %v", err)
	}
	if swept < 1 {
		t.Fatalf("sweep expired credential sessions: expected at least 1 credential swept, got %d", swept)
	}

	afterSweep := readSession(t, sessionID)
	if afterSweep.Status != "done" {
		t.Errorf("sweep: expected status done, got %q", afterSweep.Status)
	}
	if afterSweep.EndedAt == nil {
		t.Errorf("sweep: expected ended_at to be set, got NULL")
	}
	if link := readCredentialSessionLink(t, credentialID); link != nil {
		t.Errorf("sweep: expected session_id to be cleared, got %q", *link)
	}

	// Nothing is left for the next tick to pick up.
	sweptAgain, err := models.CloseExpiredCredentialSessions(t.Context(), models.DB, sweepBatch)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if sweptAgain != 0 {
		t.Errorf("second sweep: expected the sweep to be idempotent, %d credential(s) swept again", sweptAgain)
	}
}
