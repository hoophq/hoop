package models

import (
	"sort"

	"gorm.io/gorm"
)

const tableAgentTags = "private.agent_tags"

// AgentTag is one key/value pair of an agent.
type AgentTag struct {
	OrgID   string `gorm:"column:org_id"`
	AgentID string `gorm:"column:agent_id"`
	Key     string `gorm:"column:key"`
	Value   string `gorm:"column:value"`
}

// ListAgentTags returns the tags of the agents of the org, by agent ID.
// An agent without tags has no entry.
func ListAgentTags(db *gorm.DB, orgID string) (map[string]map[string]string, error) {
	var items []AgentTag
	if err := db.Table(tableAgentTags).Where("org_id = ?", orgID).Find(&items).Error; err != nil {
		return nil, err
	}
	result := map[string]map[string]string{}
	for _, t := range items {
		if result[t.AgentID] == nil {
			result[t.AgentID] = map[string]string{}
		}
		result[t.AgentID][t.Key] = t.Value
	}
	return result, nil
}

// GetAgentTags returns the tags of one agent. The map is never nil.
func GetAgentTags(db *gorm.DB, orgID, agentID string) (map[string]string, error) {
	var items []AgentTag
	err := db.Table(tableAgentTags).
		Where("org_id = ? AND agent_id = ?", orgID, agentID).
		Find(&items).Error
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for _, t := range items {
		tags[t.Key] = t.Value
	}
	return tags, nil
}

// ListAgentTagsInUse returns each distinct key/value pair on the agents of the
// org, sorted by key and value.
func ListAgentTagsInUse(db *gorm.DB, orgID string) ([]AgentTag, error) {
	var items []AgentTag
	err := db.Table(tableAgentTags).
		Distinct("key", "value").
		Where("org_id = ?", orgID).
		Order("key, value").
		Find(&items).Error
	return items, err
}

// UpdateAgentTags replaces the tags of an agent. An empty map removes them all.
// It returns gorm.ErrRecordNotFound when the org has no agent with this ID.
func UpdateAgentTags(db *gorm.DB, orgID, agentID string, tags map[string]string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// The row lock makes concurrent replaces of one agent run in sequence.
		var ids []string
		err := tx.Raw(`SELECT id FROM private.agents WHERE org_id = ? AND id = ? FOR UPDATE`,
			orgID, agentID).Scan(&ids).Error
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return gorm.ErrRecordNotFound
		}
		err = tx.Exec(`DELETE FROM private.agent_tags WHERE org_id = ? AND agent_id = ?`,
			orgID, agentID).Error
		if err != nil {
			return err
		}
		return insertAgentTags(tx, orgID, agentID, tags)
	})
}

func insertAgentTags(tx *gorm.DB, orgID, agentID string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	rows := make([]AgentTag, 0, len(tags))
	for key, value := range tags {
		rows = append(rows, AgentTag{OrgID: orgID, AgentID: agentID, Key: key, Value: value})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return tx.Table(tableAgentTags).Create(&rows).Error
}
