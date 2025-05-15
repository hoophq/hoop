package models

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type AgentStatusType string

var (
	AgentStatusConnected    AgentStatusType = "CONNECTED"
	AgentStatusDisconnected AgentStatusType = "DISCONNECTED"
)

type Agent struct {
	OrgID     string            `gorm:"column:org_id"`
	ID        string            `gorm:"column:id"`
	Name      string            `gorm:"column:name"`
	Mode      string            `gorm:"column:mode"`
	Key       string            `gorm:"column:key"`
	KeyHash   string            `gorm:"column:key_hash"`
	Status    string            `gorm:"column:status"`
	Metadata  map[string]string `gorm:"column:metadata;serializer:json"`
	UpdatedAt *string           `gorm:"column:updated_at"`
}

func (a *Agent) GetMeta(key string) (v string) {
	if len(a.Metadata) > 0 {
		if val, ok := a.Metadata[key]; ok {
			return val
		}
	}
	return
}

func (a Agent) String() string {
	return fmt.Sprintf("org=%v,name=%v,mode=%v,hostname=%v,platform=%v,version=%v,goversion=%v,kernel=%v",
		a.OrgID, a.Name, a.Mode, a.GetMeta("hostname"), a.GetMeta("platform"), a.GetMeta("version"), a.GetMeta("goversion"), a.GetMeta("kernel_version"))
}

func ListAgents(orgID string) ([]Agent, error) {
	var agentList []Agent
	return agentList, DB.Table("private.agents").
		Where("org_id = ?", orgID).Find(&agentList).Error
}

func GetAgentByNameOrID(orgID, nameOrID string) (*Agent, error) {
	var agent Agent
	err := DB.Table("private.agents").
		Where("org_id = ? AND (name = ? OR id::TEXT = ?)", orgID, nameOrID, nameOrID).
		First(&agent).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &agent, err
}

func GetAgentByToken(token string) (*Agent, error) {
	var agent Agent
	err := DB.Table("private.agents").
		Where("key_hash = ?", token).
		First(&agent).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &agent, err
}

func CreateAgentOrgKey(orgID, name, mode, key, secretKeyHash string) error {
	identifier := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{"agent", orgID, name}, "/"))).String()
	err := DB.Table("private.agents").
		Create(map[string]any{
			"id":       identifier,
			"org_id":   orgID,
			"name":     name,
			"mode":     mode,
			"key_hash": secretKeyHash,
			"key":      key,
			"status":   AgentStatusDisconnected,
			"metadata": map[string]any{},
		}).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

func CreateAgent(orgID, name, mode, secretKeyHash string) error {
	identifier := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{"agent", orgID, name}, "/"))).String()
	err := DB.Table("private.agents").
		Model(Agent{}).
		Create(map[string]any{
			"id":       identifier,
			"org_id":   orgID,
			"name":     name,
			"mode":     mode,
			"key_hash": secretKeyHash,
			"status":   AgentStatusDisconnected,
			"metadata": map[string]any{},
		}).Error
	if err == gorm.ErrDuplicatedKey {
		return ErrAlreadyExists
	}
	return err
}

// update the status of all agents and connections associated with it
func UpdateAgentStatus(orgID, agentID string, status AgentStatusType, metadata map[string]string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		updateData := map[string]any{
			"status":     status,
			"updated_at": time.Now().UTC(),
		}
		if len(metadata) > 0 {
			updateData["metadata"] = metadata
		}
		res := tx.Table("private.agents").
			Where("org_id = ? AND id = ?", orgID, agentID).
			Updates(updateData)
		if res.Error != nil {
			return res.Error
		}

		// update the status of all connections that belongs to this agent id
		connectionStatus := ConnectionStatusOnline
		if status == AgentStatusDisconnected {
			connectionStatus = ConnectionStatusOffline
		}
		return tx.Table(tableConnections).
			Where("org_id = ? AND agent_id = ?", orgID, agentID).
			Updates(map[string]any{"status": connectionStatus}).
			Error
	})
}

// update all agent resource and connections to offline status
func UpdateAllAgentsToOffline() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Table("private.agents").Updates(map[string]any{
			"status":     AgentStatusDisconnected,
			"updated_at": time.Now().UTC(),
		}).Error
		if err != nil {
			return err
		}

		return tx.Table(tableConnections).Updates(map[string]any{
			"status": ConnectionStatusOffline,
		}).Error
	})
}

func DeleteAgentByNameOrID(orgID, nameOrID string) error {
	return DB.Table("private.agents").
		Where("org_id = ? AND (name = ? OR id::TEXT = ?)", orgID, nameOrID, nameOrID).
		Delete(&Agent{}).
		Error
}

func RotateAgentSecretKey(orgID, nameOrID, secretKeyHash string) error {
	res := DB.Table("private.agents").
		Where("org_id = ? AND (name = ? OR id::TEXT = ?)", orgID, nameOrID, nameOrID).
		Updates(map[string]any{"key_hash": secretKeyHash})
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound
	}
	return res.Error
}
