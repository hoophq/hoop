package accessrequestinterceptor

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/transport/plugins/review"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

// NB12: a run of an approved one-time review records who ran it, whichever of
// the two places moves it to PROCESSING. The packet goes through the order of
// gateway/transport/client.go: this interceptor, then the review plugin.
func TestApprovedRunRecordsExecutor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	sqlDB, err := models.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	orgID := uuid.NewString()
	require.NoError(t, models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'executed-by-test')`, orgID).Error)
	plugin := review.New("http://127.0.0.1:8009")

	for _, verb := range []string{pb.ClientVerbExec, pb.ClientVerbPlainExec} {
		t.Run(verb, func(t *testing.T) {
			sid := uuid.NewString()
			require.NoError(t, models.UpsertSession(models.Session{ID: sid, OrgID: orgID, Connection: "pg-prod",
				ConnectionType: "database", Verb: "exec", Status: "ready", CreatedAt: time.Now().UTC()}))
			rev := &models.Review{ID: uuid.NewString(), OrgID: orgID, SessionID: sid, Type: models.ReviewTypeOneTime,
				Status: models.ReviewStatusApproved, ConnectionName: "pg-prod", OwnerID: "owner",
				OwnerEmail: "owner@example.com", CreatedAt: time.Now().UTC()}
			require.NoError(t, models.CreateReview(rev, "SELECT 1"))

			pctx := plugintypes.Context{OrgID: orgID, SID: sid, UserEmail: "admin@example.com", ClientVerb: verb,
				ConnectionName: "pg-prod", OrgLicenseType: license.EnterpriseType}
			pkt := &pb.Packet{Type: pbagent.SessionOpen, Spec: map[string][]byte{}}

			resp, err := OnReceive(pctx, pkt)
			require.NoError(t, err)
			require.Nil(t, resp)
			resp, err = plugin.OnReceive(pctx, pkt)
			require.NoError(t, err)
			require.Nil(t, resp)

			got, err := models.GetReviewByIdOrSid(orgID, rev.ID)
			require.NoError(t, err)
			require.Equal(t, models.ReviewStatusProcessing, got.Status)
			sess, err := models.GetSessionByID(orgID, sid)
			require.NoError(t, err)
			require.NotNil(t, sess.Review.ExecutedBy)
			require.Equal(t, "admin@example.com", *sess.Review.ExecutedBy)
		})
	}
}
