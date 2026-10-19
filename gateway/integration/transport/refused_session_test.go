//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// POST /sessions writes the session as open, then its clientexec dials Connect
// with that id. When the stream is refused before the plugins run, nothing
// else closes the row (ENG-611). The refusal closes it, and only that one:
// any other client picks its own session-id, and a session that streamed
// ends on its own.
func TestRefusedExecClosesItsOpenSession(t *testing.T) {
	c := transports()[0] // gateway-side gate; wire-agnostic, gRPC suffices
	token := adminToken(t)

	// A session's user_id is the user's subject, on the API and on Connect.
	var adminSubject string
	if err := models.DB.Raw(`SELECT subject FROM private.users WHERE org_id = ? AND email = ?`,
		gw.OrgID, testutil.FirstUserEmail).Scan(&adminSubject).Error; err != nil || adminSubject == "" {
		t.Fatalf("admin subject: %q, %v", adminSubject, err)
	}

	connection := func(prefix, agentID, update string) string {
		t.Helper()
		name := uniqueName(prefix)
		createPGConnection(t, name, agentID)
		if err := models.DB.Exec(`UPDATE private.connections SET `+update+`
			WHERE org_id = ? AND name = ?`, gw.OrgID, name).Error; err != nil {
			t.Fatalf("update connection %s: %v", name, err)
		}
		return name
	}
	onlineID, onlineDSN := createAgent(t, uniqueName("agent"))
	offlineID, _ := createAgent(t, uniqueName("agent"))
	execOff := connection("exec-off", offlineID, "access_mode_exec = 'disabled'")
	offline := connection("offline", offlineID, "access_mode_exec = 'enabled'")
	tcp := connection("tcp", onlineID, "type = 'application', subtype = 'tcp'")
	startAgent(t, c, onlineDSN)
	waitConnectionOnline(t, tcp)

	seed := func(userID, conn string, streamed bool) string {
		t.Helper()
		sid := uuid.NewString()
		if err := models.UpsertSession(models.Session{
			ID: sid, OrgID: gw.OrgID, UserID: userID, Connection: conn,
			ConnectionType: "database", Verb: pb.ClientVerbExec, Status: "open", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		if streamed {
			// The audit plugin creates the stream blob when a session starts.
			if err := models.CreateEmptySessionStreamBlob(gw.OrgID, sid, nil); err != nil {
				t.Fatalf("seed stream: %v", err)
			}
		}
		return sid
	}
	refuse := func(conn, sid, key string, want codes.Code) {
		t.Helper()
		cli, err := c.DialClient(context.Background(), ClientDialConfig{
			Token: token, ConnectionName: conn, Verb: pb.ClientVerbExec, SessionID: sid, PlainExecKey: key,
		})
		if err != nil {
			t.Fatalf("DialClient: %v", err)
		}
		defer cli.Close()
		_, err = recvUntil(cli, 30*time.Second, pbclient.SessionOpenOK)
		if status.Code(err) != want {
			t.Fatalf("%s: want %v, got %v", conn, want, err)
		}
	}
	// The gateway closes the row before it returns the refusal.
	statusOf := func(sid string) string {
		t.Helper()
		var st string
		if err := models.DB.Raw(`SELECT status FROM private.sessions WHERE org_id = ? AND id = ?`,
			gw.OrgID, sid).Scan(&st).Error; err != nil {
			t.Fatalf("read session: %v", err)
		}
		return st
	}
	key := clientexec.PlainExecSecretKey

	for _, tt := range []struct {
		msg      string
		conn     string
		owner    string
		streamed bool
		key      string
		code     codes.Code
		want     string
	}{
		{"exec disabled closes the session", execOff, adminSubject, false, key, codes.FailedPrecondition, "done"},
		{"an offline agent closes the session", offline, adminSubject, false, key, codes.FailedPrecondition, "done"},
		{"exec on a tcp connection closes the session", tcp, adminSubject, false, key, codes.InvalidArgument, "done"},
		{"a client without the gateway's key closes nothing", execOff, adminSubject, false, "", codes.FailedPrecondition, "open"},
		{"another user's session stays open", execOff, uuid.NewString(), false, key, codes.FailedPrecondition, "open"},
		{"a session that streamed stays open", execOff, adminSubject, true, key, codes.FailedPrecondition, "open"},
	} {
		sid := seed(tt.owner, tt.conn, tt.streamed)
		refuse(tt.conn, sid, tt.key, tt.code)
		if got := statusOf(sid); got != tt.want {
			t.Errorf("%s: the session reads %q, want %q", tt.msg, got, tt.want)
		}
	}
}
