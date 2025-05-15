package connectionrequests

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

var (
	connectionChecksumStore = memory.New()
	managedByAgent          = "hoopagent"
	defaultPlugins          = []string{
		plugintypes.PluginAuditName,
		plugintypes.PluginIndexName,
		plugintypes.PluginEditorName,
		plugintypes.PluginSlackName,
		plugintypes.PluginRunbooksName,
	}
)

// InvalidateSyncCache remove the connection cache sync state
// It's recommended to call this function when any process mutates the connection attributes
func InvalidateSyncCache(orgID string, connectionName string) string {
	syncKey := fmt.Sprintf("%s:%s", orgID, connectionName)
	obj := connectionChecksumStore.Pop(syncKey)
	checksum, _ := obj.(string)
	return checksum
}

func checksumCacheMatches(orgID string, req *proto.PreConnectRequest) bool {
	syncKey := fmt.Sprintf("%s:%s", orgID, req.Name)
	obj := connectionChecksumStore.Get(syncKey)
	if obj == nil {
		return false
	}
	checksumData := sha256.Sum256([]byte(req.String()))
	checksum := hex.EncodeToString(checksumData[:])
	return checksum == fmt.Sprintf("%v", obj)
}

func setChecksumCache(orgID string, req *proto.PreConnectRequest) {
	syncKey := fmt.Sprintf("%s:%s", orgID, req.Name)
	checksumData := sha256.Sum256([]byte(req.String()))
	checksum := hex.EncodeToString(checksumData[:])
	connectionChecksumStore.Set(syncKey, checksum)
}

func upsertConnection(orgID, agentID string, req *proto.PreConnectRequest, conn *models.Connection) error {
	// TODO: implement logic based on license
	if conn == nil {
		conn = &models.Connection{
			ID:        uuid.NewString(),
			OrgID:     orgID,
			AgentID:   sql.NullString{String: agentID, Valid: true},
			ManagedBy: sql.NullString{String: managedByAgent, Valid: true},
		}
	}
	if len(conn.Envs) == 0 {
		conn.Envs = map[string]string{}
	}
	conn.Command = req.Command
	conn.Name = req.Name
	conn.Type = req.Type
	conn.SubType = sql.NullString{String: req.Subtype, Valid: true}
	conn.Status = models.ConnectionStatusOnline
	conn.AccessModeConnect = "enabled"
	conn.AccessModeExec = "enabled"
	conn.AccessModeRunbooks = "enabled"
	conn.AccessSchema = "enabled"
	for key, val := range req.Envs {
		conn.Envs[key] = val
	}

	if err := models.UpsertConnection(conn); err != nil {
		return err
	}

	// best-effort operations
	models.ActivateDefaultPlugins(orgID, conn.ID)
	err := models.AddPluginConnection(orgID, plugintypes.PluginDLPName, conn.ID, req.RedactTypes)
	if err != nil {
		log.Warnf("failed adding plugin dlp connection configuration for %v, reason=%v",
			conn.Name, err)
	}
	err = models.AddPluginConnection(orgID, plugintypes.PluginReviewName, conn.ID, req.Reviewers)
	if err != nil {
		log.Warnf("failed adding plugin review connection configuration for %v, reason=%v",
			conn.Name, err)
	}
	return nil
}

func connectionSync(orgID, agentID string, req *proto.PreConnectRequest) error {
	if checksumCacheMatches(orgID, req) {
		return nil
	}
	// It is an internal operation, it must be able
	// to get the connectinon without any access control group validation
	adminCtx := models.NewAdminContext(orgID)
	conn, err := models.GetConnectionByNameOrID(adminCtx, req.Name)
	if err != nil {
		return err
	}
	// It will only sync connections that are managed by this process/agent.
	// A user could change the state of the connection and make it unmanageable
	if conn != nil {
		if conn.ManagedBy.String != managedByAgent || conn.AgentID.String != agentID {
			log.Warnf("unable to sync connection, managed-by=%v, connection-agentid=%q, requested-agentid=%q",
				conn.ManagedBy.String, conn.AgentID, agentID)
			return fmt.Errorf("connection %s is not being managed by this process, choose another name", conn.Name)
		}
	}

	// update or create a connection with new values
	if err := upsertConnection(orgID, agentID, req, conn); err != nil {
		return err
	}
	setChecksumCache(orgID, req)
	return nil
}
