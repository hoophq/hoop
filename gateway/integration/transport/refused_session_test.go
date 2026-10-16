//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// POST /sessions writes the session as open, then dials Connect with its id.
// When Connect refuses the connection, no plugin runs and nothing else closes
// the row (ENG-611). The refusal closes it, and only that one: a client picks
// its own session id, so another user's session stays open.
func TestRefusedExecClosesItsOpenSession(t *testing.T) {
	c := transports()[0] // gateway-side gate; wire-agnostic, gRPC suffices

	connName := uniqueName("exec-off")
	agentID, _ := createAgent(t, uniqueName("agent"))
	createPGConnection(t, connName, agentID)
	if err := models.DB.Exec(`UPDATE private.connections SET access_mode_exec = 'disabled'
		WHERE org_id = ? AND name = ?`, gw.OrgID, connName).Error; err != nil {
		t.Fatalf("disable exec: %v", err)
	}

	token := adminToken(t)
	// A session's user_id is the user's subject, on the API and on Connect.
	var adminSubject string
	if err := models.DB.Raw(`SELECT subject FROM private.users WHERE org_id = ? AND email = ?`,
		gw.OrgID, testutil.FirstUserEmail).Scan(&adminSubject).Error; err != nil || adminSubject == "" {
		t.Fatalf("admin subject: %q, %v", adminSubject, err)
	}

	seed := func(userID string) string {
		t.Helper()
		sid := uuid.NewString()
		if err := models.UpsertSession(models.Session{
			ID:             sid,
			OrgID:          gw.OrgID,
			UserID:         userID,
			Connection:     connName,
			ConnectionType: "database",
			Verb:           pb.ClientVerbExec,
			Status:         "open",
			CreatedAt:      time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		return sid
	}
	refuse := func(sid string) {
		t.Helper()
		cli, err := c.DialClient(context.Background(), ClientDialConfig{
			Token:          token,
			ConnectionName: connName,
			Verb:           pb.ClientVerbExec,
			SessionID:      sid,
		})
		if err != nil {
			t.Fatalf("DialClient: %v", err)
		}
		defer cli.Close()
		_, err = recvUntil(cli, 10*time.Second, pbclient.SessionOpenOK)
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("want FailedPrecondition, got %v", err)
		}
	}
	// Connect closes the row before it returns the refusal.
	statusOf := func(sid string) string {
		t.Helper()
		var st string
		if err := models.DB.Raw(`SELECT status FROM private.sessions WHERE org_id = ? AND id = ?`,
			gw.OrgID, sid).Scan(&st).Error; err != nil {
			t.Fatalf("read session: %v", err)
		}
		return st
	}

	own := seed(adminSubject)
	refuse(own)
	if got := statusOf(own); got != "done" {
		t.Errorf("the refused session reads %q, want done", got)
	}

	other := seed(uuid.NewString())
	refuse(other)
	if got := statusOf(other); got != "open" {
		t.Errorf("another user's session reads %q after a refusal that named it, want open", got)
	}
}
