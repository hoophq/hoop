package sessionapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/require"
)

// The audit plugin skips the upsert of client-api sessions, so the origin a
// provisioned session keeps is the one Provision writes.
func TestProvisionRecordsAPIOrigin(t *testing.T) {
	startAnalyzeTestDB(t, "http://127.0.0.1:1")
	const conn = "pg-provision"
	require.NoError(t, models.DB.Exec(`INSERT INTO private.resources (org_id, name, type, subtype)
		VALUES (?, ?, 'database', 'postgres')`, analyzeTestOrgID, conn).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.connections (org_id, name, type, subtype, resource_name, access_mode_exec)
		VALUES (?, ?, 'database', 'postgres', ?, 'enabled'::private.enum_access_status)`, analyzeTestOrgID, conn, conn).Error)
	require.NoError(t, models.DB.Exec(`INSERT INTO private.users (org_id, subject, email, name, status)
		VALUES (?, 'user-1', 'user1@hoop.dev', 'User One', 'active')`, analyzeTestOrgID).Error)

	rec := callSessionHandler(t, Provision, "/api/sessions/provision", map[string]any{
		"user_email": "user1@hoop.dev", "connection": conn, "script": "SELECT 1",
	})
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var resp openapi.ProvisionSessionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	sess, err := models.GetSessionByID(analyzeTestOrgID, resp.SessionID)
	require.NoError(t, err)
	require.Equal(t, proto.SessionOriginAPI, sess.Origin)
}
