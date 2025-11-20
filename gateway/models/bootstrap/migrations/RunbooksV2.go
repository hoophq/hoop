package migrations

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

func migrateOrganizationRunbooks(db *gorm.DB, orgID string) error {
	obj, err := models.GetPluginByName(orgID, plugintypes.PluginRunbooksName)
	if err != nil {
		if err == models.ErrNotFound {
			log.Infof("No runbook plugin found for org %s, skipping", orgID)
			return nil
		}

		return fmt.Errorf("failed to get plugin by name, err=%v", err)
	}

	if len(obj.EnvVars) == 0 {
		log.Infof("No runbook configuration found for org %s, only removing plugin", orgID)
		return models.DeletePlugin(db, obj)
	}

	log.Infof("Migrating runbooks for org %s", orgID)

	// Parse old Runbook Config
	gitUrlEnc := obj.EnvVars["GIT_URL"]
	gitUrl, err := base64.StdEncoding.DecodeString(gitUrlEnc)
	if err != nil {
		return fmt.Errorf("failed to decode git url, err=%v", err)
	}

	gitUserEnc := obj.EnvVars["GIT_USER"]
	gitUser, err := base64.StdEncoding.DecodeString(gitUserEnc)
	if err != nil {
		return fmt.Errorf("failed to decode git user, err=%v", err)
	}

	gitPasswordEnc := obj.EnvVars["GIT_PASSWORD"]
	gitPassword, err := base64.StdEncoding.DecodeString(gitPasswordEnc)
	if err != nil {
		return fmt.Errorf("failed to decode git password, err=%v", err)
	}

	sshKeyEnc := obj.EnvVars["GIT_SSH_KEY"]
	sshKey, err := base64.StdEncoding.DecodeString(sshKeyEnc)
	if err != nil {
		return fmt.Errorf("failed to decode ssh key, err=%v", err)
	}

	sshUserEnc := obj.EnvVars["GIT_SSH_USER"]
	sshUser, err := base64.StdEncoding.DecodeString(sshUserEnc)
	if err != nil {
		return fmt.Errorf("failed to decode ssh user, err=%v", err)
	}

	sshKeyPassEnc := obj.EnvVars["GIT_SSH_KEYPASS"]
	sshKeyPass, err := base64.StdEncoding.DecodeString(sshKeyPassEnc)
	if err != nil {
		return fmt.Errorf("failed to decode ssh key pass, err=%v", err)
	}

	sshKnownHostsEnc := obj.EnvVars["GIT_SSH_KNOWN_HOSTS"]
	sshKnownHosts, err := base64.StdEncoding.DecodeString(sshKnownHostsEnc)
	if err != nil {
		return fmt.Errorf("failed to decode ssh known hosts, err=%v", err)
	}

	hookTTLEnc := obj.EnvVars["GIT_HOOK_CONFIG_TTL"]
	hookTTLBase, err := base64.StdEncoding.DecodeString(hookTTLEnc)
	if err != nil {
		return fmt.Errorf("failed to decode hook TTL, err=%v", err)
	}

	hookTTL := 0
	if len(hookTTLBase) > 0 {
		hookTTL, err = strconv.Atoi(string(hookTTLBase))
		if err != nil {
			return fmt.Errorf("failed to parse hook TTL, err=%v", err)
		}
	}

	// Build Runbook Repository Config
	modelConfig := models.RunbookRepositoryConfig{
		GitUrl:        string(gitUrl),
		GitUser:       string(gitUser),
		GitPassword:   string(gitPassword),
		SSHKey:        string(sshKey),
		SSHUser:       string(sshUser),
		SSHKeyPass:    string(sshKeyPass),
		SSHKnownHosts: string(sshKnownHosts),
		GitHookTTL:    hookTTL,
	}

	commonConfig, err := models.BuildCommonConfig(&modelConfig)
	if err != nil {
		return fmt.Errorf("failed to build common config, err=%v", err)
	}

	repositoryName := commonConfig.GetNormalizedGitURL()

	err = models.UpsertRunbookConfiguration(db, &models.Runbooks{
		ID:    uuid.NewString(),
		OrgID: orgID,
		RepositoryConfigs: map[string]models.RunbookRepositoryConfig{
			repositoryName: modelConfig,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to upsert runbook repository, err=%v", err)
	}

	// Filter connections with non-empty config
	var connections []*models.PluginConnection
	for _, conn := range obj.Connections {
		if len(conn.Config) > 0 {
			connections = append(connections, conn)
		}
	}

	// Group connections by config path
	configToConnection := make(map[string][]string)
	for _, conn := range connections {
		key := conn.Config[0]
		configToConnection[key] = append(configToConnection[key], conn.ConnectionName)
	}

	// Create Runbook Rules
	for path, connections := range configToConnection {
		err := models.UpsertRunbookRule(db, &models.RunbookRules{
			ID:          uuid.NewString(),
			OrgID:       orgID,
			Name:        fmt.Sprintf("Rule for %s/%s", repositoryName, path),
			Description: sql.NullString{String: "Auto-migrated from Runbooks", Valid: true},
			Connections: connections,
			UserGroups:  pq.StringArray{},
			Runbooks: []models.RunbookRuleFile{{
				Repository: repositoryName,
				Name:       path,
			}},
		})
		if err != nil {
			return fmt.Errorf("failed to upsert runbook rule, err=%v", err)
		}
	}

	// Delete runbook plugin after migration
	return models.DeletePlugin(db, obj)
}

func RunRunbooksV2() error {
	log.Info("Starting Runbooks V2 migration")

	orgs, err := models.ListAllOrganizations()
	if err != nil {
		return fmt.Errorf("failed to list organizations, err=%v", err)
	}

	if len(orgs) == 0 {
		log.Info("No organizations found, done.")
		return nil
	}

	log.Infof("Found %d organizations to migrate", len(orgs))
	err = models.DB.Transaction(func(tx *gorm.DB) error {
		for _, org := range orgs {
			if err := migrateOrganizationRunbooks(tx, org.ID); err != nil {
				return fmt.Errorf("failed to migrate runbooks for org %s, err=%v", org.ID, err)
			}
		}

		return nil
	})

	return err
}
