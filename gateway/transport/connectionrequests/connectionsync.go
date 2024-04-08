package connectionrequests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
)

var (
	connectionChecksumStore = memory.New()
	managedByAgent          = "hoopagent"
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

func upsertConnection(orgID, agentID string, req *proto.PreConnectRequest, conn *pgrest.Connection) error {
	if conn == nil {
		conn = &pgrest.Connection{
			ID:        uuid.NewString(),
			OrgID:     orgID,
			AgentID:   agentID,
			ManagedBy: &managedByAgent,
		}
	}
	if len(conn.Envs) == 0 {
		conn.Envs = map[string]string{}
	}
	conn.Command = req.Command
	conn.Name = req.Name
	conn.Type = req.Type
	conn.SubType = req.Subtype
	for key, val := range req.Envs {
		conn.Envs[key] = val
	}
	// TODO: add reviews
	// TODO: add redact type plugin
	return pgconnections.New().Upsert(pgrest.NewOrgContext(orgID), *conn)
}

func connectionSync(orgID, agentID string, req *proto.PreConnectRequest) error {
	if checksumCacheMatches(orgID, req) {
		return nil
	}
	conn, err := pgconnections.New().FetchOneByNameOrID(pgrest.NewOrgContext(orgID), req.Name)
	if err != nil {
		return err
	}
	// It will only sync connections that are managed by this process/agent.
	// A user could change the state of the connection and make it unmanageable
	if conn != nil {
		var managedBy string
		if conn.ManagedBy != nil {
			managedBy = *conn.ManagedBy
		}
		if managedBy != managedByAgent || conn.AgentID != agentID {
			log.Warnf("manage inconsistency, managed-val=%q, conn-agentid=%q, request-agentid=%q",
				managedBy, conn.AgentID, agentID)
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
