package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgplugins "github.com/hoophq/hoop/gateway/pgrest/plugins"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type ErrNotFoundGuardRailRules struct {
	rules []string
}

func (e *ErrNotFoundGuardRailRules) Error() string {
	return fmt.Sprintf("unable to create guard rail connection association, the following rules were not found: %q",
		e.rules)
}

const (
	tableConnections               = "private.connections"
	tableGuardRailRulesConnections = "private.guardrail_rules_connections"

	ConnectionStatusOnline  = "online"
	ConnectionStatusOffline = "offline"
)

type Connection struct {
	OrgID               string         `gorm:"column:org_id"`
	ID                  string         `gorm:"column:id"`
	AgentID             sql.NullString `gorm:"column:agent_id"`
	Name                string         `gorm:"column:name"`
	Command             pq.StringArray `gorm:"column:command;type:text[]"`
	Type                string         `gorm:"column:type"`
	SubType             sql.NullString `gorm:"column:subtype"`
	Status              string         `gorm:"column:status"`
	ManagedBy           sql.NullString `gorm:"column:managed_by"`
	Tags                pq.StringArray `gorm:"column:_tags;type:text[]"`
	AccessModeRunbooks  string         `gorm:"column:access_mode_runbooks"`
	AccessModeExec      string         `gorm:"column:access_mode_exec"`
	AccessModeConnect   string         `gorm:"column:access_mode_connect"`
	AccessSchema        string         `gorm:"column:access_schema"`
	JiraIssueTemplateID sql.NullString `gorm:"column:jira_issue_template_id"`

	// Read Only fields
	RedactEnabled             bool              `gorm:"column:redact_enabled;->"`
	Reviewers                 pq.StringArray    `gorm:"column:reviewers;type:text[];->"`
	RedactTypes               pq.StringArray    `gorm:"column:redact_types;type:text[];->"`
	AgentMode                 string            `gorm:"column:agent_mode;->"`
	AgentName                 string            `gorm:"column:agent_name;->"`
	JiraTransitionNameOnClose sql.NullString    `gorm:"column:issue_transition_name_on_close;->"`
	Envs                      map[string]string `gorm:"column:envs;serializer:json;->"`
	GuardRailRules            pq.StringArray    `gorm:"column:guardrail_rules;type:text[];->"`
	ConnectionTags            map[string]string `gorm:"column:connection_tags;serializer:json;->"`
}

func (c Connection) AsSecrets() map[string]any {
	dst := map[string]any{}
	for k, v := range c.Envs {
		dst[k] = v
	}
	return dst
}

type EnvVars struct {
	ID    string            `gorm:"column:id"`
	OrgID string            `gorm:"column:org_id"`
	Envs  map[string]string `gorm:"column:envs;serializer:json"`
}

type ConnectionGuardRailRules struct {
	OrgID string `gorm:"column:org_id"`
	ID    string `gorm:"column:id"`
	Name  string `gorm:"column:name"`

	// Read Only Fields
	GuardRailInputRules  []byte `gorm:"column:guardrail_input_rules;->"`
	GuardRailOutputRules []byte `gorm:"column:guardrail_output_rules;->"`
}

type ConnectionJiraIssueTemplateTypes struct {
	OrgID string `gorm:"column:org_id"`
	ID    string `gorm:"column:id"`
	Name  string `gorm:"column:name"`

	// Read Only Fields
	IssueTemplatesMappingTypes []byte `gorm:"column:mapping_types;->"`
	IssueTemplatesPromptTypes  []byte `gorm:"column:prompt_types;->"`
}

func UpsertConnection(c *Connection) error {
	if c.JiraIssueTemplateID.String == "" {
		c.JiraIssueTemplateID.Valid = false
	}
	if c.Status == "" {
		c.Status = ConnectionStatusOffline
	}

	if c.AccessSchema == "" {
		c.AccessSchema = "disabled"
		if c.Type == "database" {
			c.AccessSchema = "enabled"
		}
	}

	sess := &gorm.Session{FullSaveAssociations: true}
	return DB.Session(sess).Transaction(func(tx *gorm.DB) error {
		err := tx.Table(tableConnections).
			Save(c).
			Error
		if err != nil {
			return fmt.Errorf("failed saving connections, reason=%v", err)
		}

		err = tx.Table(tableEnvVars).Save(EnvVars{OrgID: c.OrgID, ID: c.ID, Envs: c.Envs}).Error
		if err != nil {
			return fmt.Errorf("failed updating env vars from connection, reason=%v", err)
		}

		if err := updateGuardRailRules(tx, c); err != nil {
			return err
		}
		return updateBatchConnectionTags(tx, c.OrgID, c.ID, c.ConnectionTags)
	})
}

// UpsertBatchConnections updates or creates multiple connections and enable
// the default plugins for each connection
func UpsertBatchConnections(connections []*Connection) error {
	sess := &gorm.Session{FullSaveAssociations: true}
	err := DB.Session(sess).Transaction(func(tx *gorm.DB) error {
		for i, c := range connections {
			var connID string
			err := tx.Raw(`SELECT id FROM private.connections WHERE org_id = ? AND name = ?`, c.OrgID, c.Name).
				First(&connID).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed obtaining connection %v, reason=%v", c.Name, err)
			}
			connections[i].ID = connID
			if errors.Is(err, gorm.ErrRecordNotFound) {
				connections[i].ID = uuid.NewString()
			}

			err = tx.Table(tableConnections).
				Save(c).
				Error
			if err != nil {
				return fmt.Errorf("failed saving connection, reason=%v", err)
			}

			err = tx.Table(tableEnvVars).Save(EnvVars{OrgID: c.OrgID, ID: c.ID, Envs: c.Envs}).Error
			if err != nil {
				return fmt.Errorf("failed updating env vars from connection, reason=%v", err)
			}

			if err := updateBatchConnectionTags(tx, c.OrgID, c.ID, c.ConnectionTags); err != nil {
				return fmt.Errorf("failed updating connection tags, reason=%v", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, c := range connections {
		// best-effort enabling default plugins
		pgplugins.EnableDefaultPlugins(pgrest.NewOrgContext(c.OrgID), c.ID, c.Name, pgplugins.DefaultPluginNames)
	}
	return nil
}

func updateGuardRailRules(tx *gorm.DB, c *Connection) error {
	rulesAssocList := dedupeResourceNames(c.GuardRailRules)
	// remove all rules association
	err := tx.Exec(`DELETE FROM private.guardrail_rules_connections WHERE org_id = ? AND connection_id = ?`,
		c.OrgID, c.ID).Error
	if err != nil {
		return fmt.Errorf("failed cleaning guard rail rules connections, reason=%v", err)
	}

	// add new rule associations only if the rule exists
	var notFoundRules []string
	for _, ruleID := range rulesAssocList {
		var result map[string]any
		err = tx.Raw(`
			INSERT INTO private.guardrail_rules_connections (org_id, connection_id, rule_id)
			VALUES (?, ?, ?)
			RETURNING *`, c.OrgID, c.ID, ruleID).
			Scan(&result).Error
		if err != nil {
			return fmt.Errorf("failed creating guard rail association, reason=%v", err)
		}
		if len(result) == 0 {
			notFoundRules = append(notFoundRules, ruleID)
		}
	}
	if len(notFoundRules) > 0 {
		return &ErrNotFoundGuardRailRules{rules: notFoundRules}
	}
	return nil
}

func DeleteConnection(orgID, name string) error {
	return DB.Table(tableConnections).
		Where(`org_id = ? and name = ?`, orgID, name).
		Delete(&Connection{}).Error
}

func GetConnectionGuardRailRules(orgID, name string) (*ConnectionGuardRailRules, error) {
	var conn ConnectionGuardRailRules
	err := DB.Model(&ConnectionGuardRailRules{}).Raw(`
	SELECT
		c.id, c.org_id, c.name,
		(
			SELECT json_agg(r.input) FROM private.guardrail_rules r
			INNER JOIN private.guardrail_rules_connections rc ON rc.connection_id = c.id AND rc.rule_id = r.id
		) AS guardrail_input_rules,
		(
			SELECT json_agg(r.output) FROM private.guardrail_rules r
			INNER JOIN private.guardrail_rules_connections rc ON rc.connection_id = c.id AND rc.rule_id = r.id
		) AS guardrail_output_rules
	FROM private.connections c
	WHERE c.org_id = ? AND c.name = ?
	`, orgID, name).First(&conn).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &conn, nil
}

func GetConnectionByNameOrID(orgID, nameOrID string) (*Connection, error) {
	var conn Connection
	err := DB.Model(&Connection{}).Raw(`
	SELECT
		c.id, c.org_id, c.name, c.command, c.status, c.type, c.subtype, c.managed_by,
		c.access_mode_runbooks, c.access_mode_exec, c.access_mode_connect, c.access_schema,
		c.agent_id, a.name AS agent_name, a.mode AS agent_mode,
		c.jira_issue_template_id, it.issue_transition_name_on_close,
		COALESCE(c._tags, ARRAY[]::TEXT[]) AS _tags,
		( SELECT JSONB_OBJECT_AGG(ct.key, ct.value)
		 FROM private.connection_tags_association cta
		 INNER JOIN private.connection_tags ct ON ct.id = cta.tag_id
		 WHERE cta.connection_id = c.id
		 GROUP BY cta.connection_id ) AS connection_tags,
		( SELECT envs FROM public.env_vars WHERE id = c.id ) AS envs,
		COALESCE(dlpc.config, ARRAY[]::TEXT[]) AS redact_types,
		COALESCE(reviewc.config, ARRAY[]::TEXT[]) AS reviewers,
		(SELECT array_length(dlpc.config, 1) > 0) AS redact_enabled,
		COALESCE((
			SELECT array_agg(rule_id::TEXT) FROM private.guardrail_rules_connections
			WHERE private.guardrail_rules_connections.connection_id = c.id
		), ARRAY[]::TEXT[]) AS guardrail_rules
	FROM private.connections c
	LEFT JOIN private.plugins review ON review.name = 'review' AND review.org_id = @org_id
	LEFT JOIN private.plugin_connections reviewc ON reviewc.connection_id = c.id AND reviewc.plugin_id = review.id
	LEFT JOIN private.plugins dlp ON dlp.name = 'dlp' AND dlp.org_id = @org_id
	LEFT JOIN private.plugin_connections dlpc ON dlpc.connection_id = c.id AND dlpc.plugin_id = dlp.id
	LEFT JOIN private.agents a ON a.id = c.agent_id AND a.org_id = @org_id
	LEFT JOIN private.jira_issue_templates it ON it.id = c.jira_issue_template_id AND it.org_id = @org_id
	WHERE c.org_id = @org_id AND (c.name = @nameOrID OR c.id::text = @nameOrID)`,
		map[string]any{
			"org_id":   orgID,
			"nameOrID": nameOrID,
		}).
		First(&conn).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &conn, nil
}

// ConnectionOption each attribute set applies an AND operator logic
type ConnectionFilterOption struct {
	Type        string
	SubType     string
	ManagedBy   string
	AgentID     string
	Tags        []string
	TagSelector string
}

func (o ConnectionFilterOption) GetTagsAsArray() any {
	if len(o.Tags) == 0 {
		return nil
	}
	var v pq.StringArray
	for _, val := range o.Tags {
		v = append(v, val)
	}
	return v
}

func (o ConnectionFilterOption) ParseTagSelectorQuery() (selectorJsonData string, err error) {
	if o.TagSelector == "" {
		return "[]", nil
	}
	tagSelector := map[string]map[string]string{}
	for _, keyVal := range strings.Split(o.TagSelector, ",") {
		condition, key, val := map[string]string{}, "", ""
		switch {
		case strings.Contains(keyVal, "!="):
			key, val, _ = strings.Cut(keyVal, "!=")
			condition["op"] = "!="
		case strings.Contains(keyVal, "="):
			condition["op"] = "="
			key, val, _ = strings.Cut(keyVal, "=")
		default:
			return "", fmt.Errorf("could not find any valid operator, accepted values are '=', '!='")
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		condition["key"] = key
		condition["val"] = val
		tagSelector[key] = condition
	}

	var result []map[string]string
	for _, conditionMap := range tagSelector {
		result = append(result, conditionMap)
	}

	jsonData, err := json.Marshal(&result)
	if err != nil {
		return "", fmt.Errorf("unable to encode tag selector, reason=%v", err)
	}
	if len(tagSelector) == 0 {
		return "[]", nil
	}
	return string(jsonData), nil
}

func ListConnections(orgID string, opts ConnectionFilterOption) ([]Connection, error) {
	setConnectionOptionDefaults(&opts)
	tagSelectorJsonData, err := opts.ParseTagSelectorQuery()
	if err != nil {
		return nil, err
	}
	tagsAsArray := opts.GetTagsAsArray()
	var items []Connection
	err = DB.Raw(`
	WITH tag_selector_keys(key, op, val) AS (
		SELECT * FROM json_to_recordset(?::JSON) AS x(key TEXT, op TEXT, val TEXT)
	)
	SELECT
		c.id, c.org_id, c.agent_id, c.name, c.command, c.status, c.type, c.subtype, c.managed_by,
		c.access_mode_runbooks, c.access_mode_exec, c.access_mode_connect, c.access_schema,
		c.jira_issue_template_id,
		-- legacy tags
		COALESCE(c._tags, ARRAY[]::TEXT[]) AS _tags,
		( SELECT JSONB_OBJECT_AGG(ct.key, ct.value)
		 FROM private.connection_tags_association cta
		 INNER JOIN private.connection_tags ct ON ct.id = cta.tag_id
		 WHERE cta.connection_id = c.id
		 GROUP BY cta.connection_id ) AS connection_tags,
		( SELECT envs FROM public.env_vars WHERE id = c.id ) AS envs,
		COALESCE(dlpc.config, ARRAY[]::TEXT[]) AS redact_types,
		COALESCE(reviewc.config, ARRAY[]::TEXT[]) AS reviewers,
		(SELECT array_length(dlpc.config, 1) > 0) AS redact_enabled,
		COALESCE((
			SELECT array_agg(rule_id::TEXT) FROM private.guardrail_rules_connections
			WHERE private.guardrail_rules_connections.connection_id = c.id
		), ARRAY[]::TEXT[]) AS guardrail_rules
	FROM private.connections c
	LEFT JOIN private.plugins review ON review.name = 'review' AND review.org_id = ?
	LEFT JOIN private.plugin_connections reviewc ON reviewc.connection_id = c.id AND reviewc.plugin_id = review.id
	LEFT JOIN private.plugins dlp ON dlp.name = 'dlp' AND dlp.org_id = ?
	LEFT JOIN private.plugin_connections dlpc ON dlpc.connection_id = c.id AND dlpc.plugin_id = dlp.id
	WHERE c.org_id = ? AND
	(
		COALESCE(c.type::text, '') LIKE ? AND
		COALESCE(c.subtype, '') LIKE ? AND
		COALESCE(c.agent_id::text, '') LIKE ? AND
		COALESCE(c.managed_by, '') LIKE ? AND
		-- legacy tags
		CASE WHEN (?)::text[] IS NOT NULL
			THEN c._tags @> (?)::text[]
			ELSE true
		END AND
		(
			-- return all results if no tag selectors provided
			(SELECT COUNT(*) FROM tag_selector_keys) = 0
			OR
			-- AND logic: each tag selector criterion must be satisfied
			NOT EXISTS (
				-- Find any tag selector that is NOT satisfied by this connection
				SELECT 1 FROM tag_selector_keys tsk
				WHERE NOT EXISTS (
				SELECT 1
				FROM private.connection_tags_association cta
				JOIN private.connection_tags ct ON ct.id = cta.tag_id
				WHERE cta.connection_id = c.id
				AND ct.key = tsk.key
				AND CASE
					WHEN tsk.op = '=' THEN ct.value = tsk.val
					WHEN tsk.op = '!=' THEN ct.value != tsk.val
					ELSE false
				END
				)
			)
		)
	) ORDER BY c.name ASC`,
		tagSelectorJsonData,
		orgID, orgID, orgID,
		opts.Type,
		opts.SubType,
		opts.AgentID,
		opts.ManagedBy,
		tagsAsArray, tagsAsArray,
	).Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}

func setConnectionOptionDefaults(opts *ConnectionFilterOption) {
	if opts.AgentID == "" {
		opts.AgentID = "%"
	}
	if opts.Type == "" {
		opts.Type = "%"
	}
	if opts.SubType == "" {
		opts.SubType = "%"
	}
	if opts.ManagedBy == "" {
		opts.ManagedBy = "%"
	}
}

func dedupeResourceNames(resourceNames []string) (v []string) {
	m := map[string]any{}
	for _, name := range resourceNames {
		m[name] = nil
	}
	for name := range m {
		if name == "" {
			continue
		}
		v = append(v, name)
	}
	return v
}
