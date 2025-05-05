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
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	"github.com/hoophq/hoop/gateway/storagev2/types"
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

func upsertConnection(ctx pgrest.OrgContext, agentID string, req *proto.PreConnectRequest, conn *models.Connection) error {
	// TODO: implement logic based on license
	if conn == nil {
		conn = &models.Connection{
			ID:        uuid.NewString(),
			OrgID:     ctx.GetOrgID(),
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
	pgplugins.EnableDefaultPlugins(ctx, conn.ID, req.Name, defaultPlugins)
	pgplugins.UpsertPluginConnection(ctx, plugintypes.PluginDLPName, &types.PluginConnection{
		ID:           uuid.NewString(),
		ConnectionID: conn.ID,
		Name:         req.Name,
		Config:       req.RedactTypes,
	})
	pgplugins.UpsertPluginConnection(ctx, plugintypes.PluginReviewName, &types.PluginConnection{
		ID:           uuid.NewString(),
		ConnectionID: conn.ID,
		Name:         req.Name,
		Config:       req.Reviewers,
	})
	return nil
}

func connectionSync(ctx pgrest.OrgContext, agentID string, req *proto.PreConnectRequest) error {
	if checksumCacheMatches(ctx.GetOrgID(), req) {
		return nil
	}
	conn, err := models.GetConnectionByNameOrID(ctx.GetOrgID(), req.Name)
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
	if err := upsertConnection(ctx, agentID, req, conn); err != nil {
		return err
	}
	setChecksumCache(ctx.GetOrgID(), req)
	return nil
}
