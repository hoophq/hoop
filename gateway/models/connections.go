package models

import (
	"database/sql"
	"errors"
	"fmt"

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
	tableEnvVars                   = "private.env_vars"
	tableGuardRailRulesConnections = "private.guardrail_rules_connections"

	ConnectionStatusOnline  = "online"
	ConnectionStatusOffline = "offline"
)

type Connection struct {
	OrgID              string            `gorm:"column:org_id"`
	ID                 string            `gorm:"column:id"`
	AgentID            sql.NullString    `gorm:"column:agent_id"`
	Name               string            `gorm:"column:name"`
	Command            pq.StringArray    `gorm:"column:command;type:text[]"`
	Type               string            `gorm:"column:type"`
	SubType            sql.NullString    `gorm:"column:subtype"`
	Status             string            `gorm:"column:status"`
	ManagedBy          sql.NullString    `gorm:"column:managed_by"`
	Tags               pq.StringArray    `gorm:"column:_tags;type:text[]"`
	AccessModeRunbooks string            `gorm:"column:access_mode_runbooks"`
	AccessModeExec     string            `gorm:"column:access_mode_exec"`
	AccessModeConnect  string            `gorm:"column:access_mode_connect"`
	AccessSchema       string            `gorm:"column:access_schema"`
	Envs               map[string]string `gorm:"column:envs;serializer:json;->"`
	GuardRailRules     pq.StringArray    `gorm:"column:guardrail_rules;type:text[];->"`

	// Read Only fields
	GuardRailInputRules  []byte         `gorm:"column:guardrail_input_rules;->"`
	GuardRailOutputRules []byte         `gorm:"column:guardrail_output_rules;->"`
	RedactEnabled        bool           `gorm:"column:redact_enabled;->"`
	Reviewers            pq.StringArray `gorm:"column:reviewers;type:text[];->"`
	RedactTypes          pq.StringArray `gorm:"column:redact_types;type:text[];->"`
	AgentMode            string         `gorm:"column:agent_mode;->"`
	AgentName            string         `gorm:"column:agent_name;->"`
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

func UpsertConnection(c *Connection) error {
	if c.Status == "" {
		c.Status = ConnectionStatusOffline
	}

	if c.AccessSchema == "" {
		c.AccessSchema = "disabled"
		if c.Type == "database" {
			c.AccessSchema = "enabled"
		}
	}

	rulesAssocList := dedupeGuardRailRules(c.GuardRailRules)
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

		// remove all rules association
		err = tx.Exec(`DELETE FROM private.guardrail_rules_connections WHERE org_id = ? AND connection_id = ?`,
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
	})
}

func DeleteConnection(orgID, name string) error {
	return DB.Table(tableConnections).
		Where(`org_id = ? and name = ?`, orgID, name).
		Delete(&Connection{}).Error
}

func GetConnectionByNameOrID(orgID, nameOrID string) (*Connection, error) {
	var conn Connection
	err := DB.Debug().Model(&Connection{}).Raw(`
	SELECT
		c.id, c.org_id, c.name, c.command, c.status, c.type, c.subtype, c.managed_by,
		c.access_mode_runbooks, c.access_mode_exec, c.access_mode_connect, c.access_schema,
		c.agent_id, a.name AS agent_name, a.mode AS agent_mode,
		COALESCE(c._tags, ARRAY[]::TEXT[]) AS _tags,
		( SELECT envs FROM public.env_vars WHERE id = c.id ) AS envs,
		COALESCE(dlpc.config, ARRAY[]::TEXT[]) AS redact_types,
		COALESCE(reviewc.config, ARRAY[]::TEXT[]) AS reviewers,
		(SELECT array_length(dlpc.config, 1) > 0) AS redact_enabled,
		COALESCE((
			SELECT array_agg(id::TEXT) FROM private.guardrail_rules_connections
		), ARRAY[]::TEXT[]) AS guardrail_rules,
		(
			SELECT json_agg(r.input) FROM private.guardrail_rules r
			INNER JOIN private.guardrail_rules_connections rc ON rc.connection_id = c.id AND rc.rule_id = r.id
		) AS guardrail_input_rules,
		(
			SELECT json_agg(r.output) FROM private.guardrail_rules r
			INNER JOIN private.guardrail_rules_connections rc ON rc.connection_id = c.id AND rc.rule_id = r.id
		) AS guardrail_output_rules
	FROM private.connections c
	LEFT JOIN private.plugins review ON review.name = 'review'
	LEFT JOIN private.plugin_connections reviewc ON reviewc.connection_id = c.id AND reviewc.plugin_id = review.id
	LEFT JOIN private.plugins dlp ON dlp.name = 'dlp'
	LEFT JOIN private.plugin_connections dlpc ON dlpc.connection_id = c.id AND dlpc.plugin_id = dlp.id
	LEFT JOIN private.agents a ON a.id = c.agent_id
	WHERE c.org_id = ? AND (c.name = ? OR c.id::text = ?)`,
		orgID, nameOrID, nameOrID).
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
	Type      string
	SubType   string
	ManagedBy string
	AgentID   string
	Tags      []string
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

func ListConnections(orgID string, opts ConnectionFilterOption) ([]Connection, error) {
	setConnectionOptionDefaults(&opts)
	var items []Connection
	err := DB.Raw(`
	SELECT
		c.id, c.org_id, c.agent_id, c.name, c.command, c.status, c.type, c.subtype, c.managed_by,
		c.access_mode_runbooks, c.access_mode_exec, c.access_mode_connect, c.access_schema,
		COALESCE(c._tags, ARRAY[]::TEXT[]) AS _tags,
		( SELECT envs FROM public.env_vars WHERE id = c.id ) AS envs,
		COALESCE(dlpc.config, ARRAY[]::TEXT[]) AS redact_types,
		COALESCE(reviewc.config, ARRAY[]::TEXT[]) AS reviewers,
		(SELECT array_length(dlpc.config, 1) > 0) AS redact_enabled,
		COALESCE((
			SELECT array_agg(id::TEXT) FROM private.guardrail_rules_connections
		), ARRAY[]::TEXT[]) AS guardrail_rules
	FROM private.connections c
	LEFT JOIN private.plugins review ON review.name = 'review'
	LEFT JOIN private.plugin_connections reviewc ON reviewc.connection_id = c.id AND reviewc.plugin_id = review.id
	LEFT JOIN private.plugins dlp ON dlp.name = 'dlp'
	LEFT JOIN private.plugin_connections dlpc ON dlpc.connection_id = c.id AND dlpc.plugin_id = dlp.id
	WHERE c.org_id = @org_id AND
	(
		COALESCE(c.type::text, '') LIKE @type AND
		COALESCE(c.subtype, '') LIKE @subtype AND
		COALESCE(c.agent_id::text, '') LIKE @agent_id AND
		COALESCE(c.managed_by, '') LIKE @managed_by AND
		CASE WHEN (@tags)::text[] IS NOT NULL
			THEN c._tags @> (@tags)::text[]
			ELSE true
		END
	) ORDER BY c.name ASC`,
		map[string]any{
			"org_id":     orgID,
			"type":       opts.Type,
			"subtype":    opts.SubType,
			"agent_id":   opts.AgentID,
			"managed_by": opts.ManagedBy,
			"tags":       opts.GetTagsAsArray(),
		},
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

func dedupeGuardRailRules(resourceNames []string) (v []string) {
	m := map[string]any{}
	for _, name := range resourceNames {
		m[name] = nil
	}
	for name := range m {
		v = append(v, name)
	}
	return v
}
