package services

import (
	"errors"
	"fmt"

	"github.com/hoophq/hoop/common/dsnkeys"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
)

// StandaloneAgentName is the dedicated agent record used by the standalone
// mode (`hoop start standalone`). Its secret is stored recoverable in the
// agents table (the same pattern used by the `_default` org key), so every
// boot reconstructs the same DSN without persisting credentials outside the
// database.
const StandaloneAgentName = "standalone"

// StandaloneAgentDSN returns the connection DSN for the standalone agent,
// creating its record on first boot. The secret is stored recoverable in
// the agents table — the database is the only credential store, mirroring
// the `_default` org key pattern (gateway/api/orgs). Subsequent calls
// return the same DSN, which is what makes standalone reboots credential-
// stable.
//
// It requires a single-tenant gateway: the standalone agent belongs to the
// only organization, and multi-tenant deployments must provision agents
// explicitly.
func StandaloneAgentDSN(grpcURL string) (string, error) {
	orgs, err := models.ListAllOrganizations()
	if err != nil {
		return "", fmt.Errorf("failed listing organizations: %w", err)
	}
	if len(orgs) != 1 {
		return "", fmt.Errorf("standalone mode requires a single-tenant gateway, found %d organizations", len(orgs))
	}
	orgID := orgs[0].ID

	ag, err := models.GetAgentByNameOrID(orgID, StandaloneAgentName)
	switch {
	case errors.Is(err, models.ErrNotFound):
		secretKey, secretKeyHash, err := keys.GenerateSecureRandomKey("", 32)
		if err != nil {
			return "", fmt.Errorf("failed generating agent secret: %w", err)
		}
		err = models.CreateAgentOrgKey(orgID, StandaloneAgentName, proto.AgentModeStandardType, secretKey, secretKeyHash)
		switch {
		case errors.Is(err, models.ErrAlreadyExists):
			// A concurrent provisioner won the insert between our read and
			// create. The record is the source of truth either way: reload
			// it and return the winner's stored key so every caller ends up
			// with the same DSN.
			if ag, err = models.GetAgentByNameOrID(orgID, StandaloneAgentName); err != nil {
				return "", fmt.Errorf("standalone agent was created concurrently but could not be reloaded: %w", err)
			}
		case err != nil:
			return "", fmt.Errorf("failed creating the standalone agent record: %w", err)
		default:
			return dsnkeys.NewString(grpcURL, StandaloneAgentName, secretKey, proto.AgentModeStandardType)
		}
	case err != nil:
		return "", fmt.Errorf("failed fetching the standalone agent record: %w", err)
	}
	if ag.Key == "" {
		return "", fmt.Errorf("an agent named %q already exists without a recoverable key; "+
			"remove it (hoop admin delete agent %s) and run standalone again", StandaloneAgentName, StandaloneAgentName)
	}
	return dsnkeys.NewString(grpcURL, StandaloneAgentName, ag.Key, proto.AgentModeStandardType)
}
