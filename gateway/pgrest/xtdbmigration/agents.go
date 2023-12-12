package xtdbmigration

import (
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	"github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
)

func migrateAgents(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating agents")
	agentStore := agent.Storage{Storage: storage.New()}
	agentStore.SetURL(xtdbURL)
	agentList, err := agentStore.FindAll(user.NewContext(orgID, ""))
	if err != nil {
		log.Warnf("pgrest migration: failed listing agents, err=%v", err)
		return
	}
	var state migrationState
	for _, a := range agentList {
		if a.Mode == "" {
			log.Warnf("pgrest migration: agent %s, id=%s has no mode, skipping", a.Name, a.Id)
			continue
		}
		err := pgagents.New().Upsert(&pgrest.Agent{
			ID:     a.Id,
			OrgID:  a.OrgId,
			Name:   a.Name,
			Mode:   a.Mode,
			Token:  a.Token,
			Status: string(a.Status),
			Metadata: map[string]string{
				"hostname":       a.Hostname,
				"platform":       a.Platform,
				"goversion":      a.GoVersion,
				"version":        a.Version,
				"kernel_version": a.KernelVersion,
				"compiler":       a.Compiler,
				"machine_id":     a.MachineId,
			},
		})
		if err != nil {
			log.Warnf("pgrest migration: failed creating agent=%v, err=%v", a.Id, err)
			state.failed++
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: agents migrated, total=%v, success=%d, failed=%d", len(agentList), state.success, state.failed)
}
