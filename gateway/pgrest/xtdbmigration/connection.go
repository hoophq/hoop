package xtdbmigration

import (
	"fmt"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	"github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
)

func migrateConnections(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating connections")
	connStore := connection.Storage{Storage: storage.New()}
	connStore.SetURL(xtdbURL)
	connList, err := connStore.FindAll(user.NewContext(orgID, ""))
	if err != nil {
		log.Warnf("pgrest migration: failed listing connections, err=%v", err)
		return
	}
	var state migrationState
	orgCtx := user.NewContext(orgID, "")
	for _, c := range connList {
		conn, err := connStore.FindOne(orgCtx, c.Name)
		if err != nil || conn == nil {
			log.Warnf("pgrest migration: failed fetching connection=%v, err=%v", c.Name, err)
			state.failed++
			continue
		}
		envs := map[string]string{}
		for key, val := range conn.Secret {
			envs[key] = fmt.Sprintf("%v", val)
		}
		pgconnections.New().Upsert(orgCtx, pgrest.Connection{
			ID:            c.Id,
			OrgID:         orgID,
			AgentID:       c.AgentId,
			LegacyAgentID: c.AgentId,
			Name:          c.Name,
			Command:       c.Command,
			Type:          string(c.Type),
			Envs:          envs,
		})
		if err != nil {
			log.Warnf("pgrest migration: failed creating connection=%v, err=%v", c.Id, err)
			state.failed++
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: connections migrated, total=%v, success=%d, failed=%d", len(connList), state.success, state.failed)
}
