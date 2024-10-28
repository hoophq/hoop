package models

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

const (
	tableConnections               = "private.connections"
	tableEnvVars                   = "private.env_vars"
	tableGuardRailRulesConnections = "private.guardrail_rules_connections"

	ConnectionStatusOnline  = "online"
	ConnectionStatusOffline = "offline"
)

type Connection struct {
	OrgID              string         `gorm:"column:org_id"`
	ID                 string         `gorm:"column:id"`
	AgentID            sql.NullString `gorm:"column:agent_id"`
	Name               string         `gorm:"column:name"`
	Command            pq.StringArray `gorm:"column:command;type:text[]"`
	Type               string         `gorm:"column:type"`
	SubType            sql.NullString `gorm:"column:subtype"`
	Status             string         `gorm:"column:status"`
	ManagedBy          sql.NullString `gorm:"column:managed_by"`
	Tags               pq.StringArray `gorm:"column:_tags;type:text[]"`
	AccessModeRunbooks string         `gorm:"column:access_mode_runbooks"`
	AccessModeExec     string         `gorm:"column:access_mode_exec"`
	AccessModeConnect  string         `gorm:"column:access_mode_connect"`
	AccessSchema       string         `gorm:"column:access_schema"`

	// TODO: add created at
	// TODO: add updated at

	Envs            EnvVars          `gorm:"foreignKey:id;joinForeignKey:id"`
	GuardRailRules  []string         `gorm:"-"`
	GuardRailRules2 []GuardRailRules `gorm:"many2many:guardrail_rules"`

	// read only attributes
	// Org              Org                `json:"orgs"`
	// PluginConnection []PluginConnection `json:"plugin_connections"`
	// Agent            Agent              `json:"agents"`
}

type GuardRailRulesConnection struct {
	OrgID        string    `gorm:"column:org_id"`
	ID           string    `gorm:"column:id"`
	RuleID       string    `gorm:"column:rule_id"`
	ConnectionID string    `gorm:"column:connection_id"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

type EnvVars struct {
	ID    string            `gorm:"column:id"`
	OrgID string            `gorm:"column:org_id"`
	Envs  map[string]string `gorm:"column:envs;serializer:json"`
}

func UpsertConnection(c *Connection) error {
	// var subType *string
	// if c.SubType != "" {
	// 	subType = &c.SubType
	// }
	if c.Status == "" {
		c.Status = ConnectionStatusOffline
	}
	// c.AgentID = *toAgentID(c.AgentID)

	if c.AccessSchema == "" {
		c.AccessSchema = "disabled"
		if c.Type == "database" {
			c.AccessSchema = "enabled"
		}
	}

	rulesAssocList := dedupeGuardRailRules(c.GuardRailRules)
	_ = rulesAssocList
	sess := &gorm.Session{FullSaveAssociations: true}
	return DB.Debug().Session(sess).Transaction(func(tx *gorm.DB) error {
		err := tx.Table(tableConnections).
			Save(c).
			Error
		if err != nil {
			return fmt.Errorf("failed saving connections, reason=%v", err)
		}

		// remove all rules association
		err = tx.Exec(`DELETE FROM private.guardrail_rules_connections WHERE org_id = ? and connection_id = ?`,
			c.OrgID, c.ID).Error
		if err != nil {
			return fmt.Errorf("failed cleaning guard rail rules connections, reason=%v", err)
		}

		// add new rule associations if it exists
		for _, ruleName := range rulesAssocList {
			err = tx.Exec(`
			WITH rules AS ( SELECT id FROM private.guardrail_rules WHERE name = ? )
			INSERT INTO private.guardrail_rules_connections (org_id, connection_id, rule_id)
			SELECT ?, ?, ( SELECT id FROM rules )
			WHERE EXISTS ( SELECT id FROM rules )`,
				ruleName, c.OrgID, c.ID).Error
			if err != nil {
				return fmt.Errorf("failed creating guard rail association, reason=%v", err)
			}
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
	var c Connection
	err := DB.Table(tableConnections).Debug().
		Model(&Connection{}).
		Joins("Envs").
		Where("connections.org_id = ? and connections.name = ?", orgID, nameOrID).
		Find(&c).Error
	if err != nil {
		fmt.Printf("failed obtaining connection: %v\n", err)
		return nil, err
	}
	fmt.Printf("connection--->>> %#v\n", c)
	return &c, nil
}

type ConnectionOption struct {
	key string
	val string
}

var availableOptions = map[string]string{
	"type":       "string",
	"subtype":    "string",
	"managed_by": "string",
	"agent_id":   "string",
	"tags":       "array",
}

func ListConnections(orgID string, opts ...*ConnectionOption) {

}

// func toAgentID(agentID string) (v *string) {
// 	if _, err := uuid.Parse(agentID); err == nil {
// 		return &agentID
// 	}
// 	return
// }

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
