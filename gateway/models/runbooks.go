package models

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	commonRunbooks "github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RunbookRepositoryConfig struct {
	GitUrl        string `json:"git_url"`
	GitUser       string `json:"git_user"`
	GitPassword   string `json:"git_password"`
	SSHKey        string `json:"ssh_key"`
	SSHUser       string `json:"ssh_user"`
	SSHKeyPass    string `json:"ssh_key_pass"`
	SSHKnownHosts string `json:"ssh_known_hosts"`
	GitHookTTL    int    `json:"git_hook_config_ttl"`
	GitBranch     string `json:"git_branch"`
}

type Runbooks struct {
	ID                string                             `gorm:"column:id"`
	OrgID             string                             `gorm:"column:org_id"`
	RepositoryConfigs map[string]RunbookRepositoryConfig `gorm:"column:repository_configs;serializer:json"`
	CreatedAt         time.Time                          `gorm:"column:created_at"`
	UpdatedAt         time.Time                          `gorm:"column:updated_at"`
}

type RunbookRuleFile struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
}

type RunbookRuleFiles []RunbookRuleFile

func (r RunbookRuleFiles) Value() (driver.Value, error) {
	return json.Marshal(r)
}
func (r *RunbookRuleFiles) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan RunbookRuleFiles")
	}
	return json.Unmarshal(bytes, r)
}

type RunbookRules struct {
	ID          string           `gorm:"column:id"`
	OrgID       string           `gorm:"column:org_id"`
	Name        string           `gorm:"column:name"`
	Description sql.NullString   `gorm:"column:description"`
	UserGroups  pq.StringArray   `gorm:"column:user_groups;type:text[]"`
	Connections pq.StringArray   `gorm:"column:connections;type:text[]"`
	Runbooks    RunbookRuleFiles `gorm:"column:runbooks;type:jsonb;serializer:json"`
	CreatedAt   time.Time        `gorm:"column:created_at"`
	UpdatedAt   time.Time        `gorm:"column:updated_at"`
}

func IsUserAllowedToRunRunbook(orgId, connection, runbookRepository, runbookName string, userGroups []string) (bool, error) {
	if slices.Contains(userGroups, types.GroupAdmin) {
		return true, nil
	}

	existsRules := false
	err := DB.Table("private.runbook_rules").
		Select("1").
		Where("org_id = ?", orgId).
		Limit(1).
		Find(&existsRules).Error
	if err != nil {
		return false, err
	}

	if !existsRules {
		return true, nil
	}

	var count int64
	err = DB.
		Table("private.runbook_rules").
		Where(`org_id = ? AND
		(CARDINALITY(user_groups) = 0 OR user_groups && ?) AND
		(CARDINALITY(connections) = 0 OR connections && ?) AND
		(JSONB_ARRAY_LENGTH(runbooks) = 0 OR EXISTS (
			SELECT 1
			FROM JSONB_ARRAY_ELEMENTS(runbooks) file
			WHERE file ->> 'repository' = ? AND (file ->> 'name' = '' OR ? ILIKE (file ->> 'name') || '%') LIMIT 1
		))`, orgId, pq.StringArray(userGroups), pq.StringArray([]string{connection}), runbookRepository, runbookName).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func GetRunbookRules(db *gorm.DB, orgID string, offset int, limit int) ([]RunbookRules, error) {
	query := db.Table("private.runbook_rules").Where("org_id = ?", orgID)
	if limit > 0 {
		query = query.Offset(offset).Limit(limit)
	}

	var runbookRules []RunbookRules
	err := query.Find(&runbookRules).Error
	if err != nil {
		return nil, err
	}
	return runbookRules, nil
}

func GetRunbookConfigurationByOrgID(db *gorm.DB, orgID string) (*Runbooks, error) {
	var runbooks Runbooks
	err := db.Table("private.runbooks").Where("org_id = ?", orgID).First(&runbooks).Error
	if err != nil {
		return nil, err
	}
	return &runbooks, nil
}

func DeleteRunbookConfigurationByOrgID(db *gorm.DB, orgID string) error {
	return db.Table("private.runbooks").Where("org_id = ?", orgID).Delete(&Runbooks{}).Error
}

// CreateRunbookConfigurationEntry creates a single runbook repository configuration entry
// In case the resource doesn't exists, it creates a new one with the new entry
func CreateRunbookConfigurationEntry(db *gorm.DB, orgID, repositoryKey string, newConfig *RunbookRepositoryConfig) error {
	configJson, _ := json.Marshal(newConfig)
	res := db.Exec(`
	INSERT INTO private.runbooks as r (org_id, repository_configs)
	VALUES ((@org_id)::UUID, JSONB_BUILD_OBJECT((@repository_key)::TEXT, (@config_json)::JSONB))
	ON CONFLICT (org_id) DO UPDATE
	SET
		repository_configs = JSONB_SET(
			COALESCE(r.repository_configs, '{}'::jsonb),
			ARRAY[(@repository_key)::TEXT],
			(@config_json)::jsonb
		),
		updated_at = NOW()
		WHERE NOT JSONB_EXISTS(r.repository_configs, @repository_key)`,
		map[string]any{
			"org_id":         orgID,
			"repository_key": repositoryKey,
			"config_json":    string(configJson),
		},
	)
	// it should only insert a new entry or add non existent config entries
	if res.RowsAffected == 0 {
		return ErrAlreadyExists
	}
	return res.Error
}

// UpdateRunbookConfigurationEntry updates an existing runbook repository configuration entry
func UpdateRunbookConfigurationEntry(db *gorm.DB, orgID, repositoryKey string, newConfig *RunbookRepositoryConfig) error {
	configJson, _ := json.Marshal(newConfig)
	res := db.Exec(`
		UPDATE private.runbooks
		SET
			repository_configs = JSONB_SET(
				COALESCE(repository_configs, '{}'::JSONB),
				ARRAY[(@repository_key)::TEXT],
				(@config_json)::jsonb
			),
			updated_at = NOW()
		WHERE org_id = @org_id
		AND JSONB_EXISTS(repository_configs, @repository_key)`,
		map[string]any{
			"org_id":         orgID,
			"repository_key": repositoryKey,
			"config_json":    string(configJson),
		})
	// it should only update existing config
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

// DeleteRunbookConfigurationEntry deletes an existing runbook repository configuration entry
func DeleteRunbookConfigurationEntry(db *gorm.DB, orgID, id string) error {
	res := db.Exec(`
	UPDATE private.runbooks
	SET
		-- removes keys from JSONB object where the key matches the generated UUID from the git_url
		repository_configs = repository_configs - (
			SELECT key
			FROM JSONB_EACH(repository_configs) AS entry(key, value)
			WHERE private.uuid_generate_v5(
				private.uuid_ns_url(),
				value->>'git_url'
			) = @git_url_id
			LIMIT 1
		),
		updated_at = NOW()
	WHERE org_id = @org_id
	-- checks if a matching repository exists before attempting the update
	AND EXISTS (
		SELECT 1
		FROM JSONB_EACH(repository_configs) AS entry(key, value)
		WHERE private.uuid_generate_v5(
			private.uuid_ns_url(),
			value->>'git_url'
		) = @git_url_id
	)`, map[string]any{
		"org_id":     orgID,
		"git_url_id": id,
	})
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}

func UpsertRunbookConfiguration(db *gorm.DB, runbooks *Runbooks) error {
	tx := db.Table("private.runbooks").Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "org_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"repository_configs": runbooks.RepositoryConfigs,
			"updated_at":         time.Now().UTC(),
		}),
	}, clause.Returning{}).Create(runbooks)

	return tx.Error
}

func GetRunbookRuleByID(db *gorm.DB, orgID, ruleID string) (*RunbookRules, error) {
	var rule RunbookRules
	err := db.Table("private.runbook_rules").Where("org_id = ? AND id = ?", orgID, ruleID).First(&rule).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func UpsertRunbookRule(db *gorm.DB, rule *RunbookRules) error {
	return db.Table("private.runbook_rules").
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"name":        rule.Name,
				"description": rule.Description,
				"user_groups": rule.UserGroups,
				"connections": rule.Connections,
				"runbooks":    rule.Runbooks,
				"updated_at":  time.Now().UTC(),
			}),
		}).
		Create(rule).
		Error
}

func DeleteRunbookRule(db *gorm.DB, orgID, ruleID string) error {
	return db.Table("private.runbook_rules").
		Where("id = ? AND org_id = ?", ruleID, orgID).
		Delete(&RunbookRules{}).
		Error
}

func BuildCommonConfig(config *RunbookRepositoryConfig) (*commonRunbooks.Config, error) {
	configInput := &commonRunbooks.ConfigInput{
		GitURL:        config.GitUrl,
		GitUser:       config.GitUser,
		GitPassword:   config.GitPassword,
		SSHKey:        config.SSHKey,
		SSHUser:       config.SSHUser,
		SSHKeyPass:    config.SSHKeyPass,
		SSHKnownHosts: config.SSHKnownHosts,
		GitBranch:     config.GitBranch,
		HookCacheTTL:  config.GitHookTTL,
	}

	return commonRunbooks.NewConfigV2(configInput)
}

func CreateDefaultRunbookConfiguration(db *gorm.DB, orgID string) (*Runbooks, error) {
	const defaultRepoURI = "https://github.com/hoophq/demo-runbooks"
	const defaultRepoName = "github.com/hoophq/demo-runbooks"

	// Check if runbook configuration already exists for this org
	existing, _ := GetRunbookConfigurationByOrgID(db, orgID)
	if existing != nil {
		return existing, nil
	}

	runbooks := &Runbooks{
		ID:    uuid.NewString(),
		OrgID: orgID,
		RepositoryConfigs: map[string]RunbookRepositoryConfig{
			defaultRepoName: {
				GitUrl:    defaultRepoURI,
				GitBranch: "main",
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := UpsertRunbookConfiguration(db, runbooks); err != nil {
		return nil, fmt.Errorf("failed to create default runbook configuration: %w", err)
	}

	return runbooks, nil
}
