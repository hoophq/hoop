package models

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

// AgentSPIFFEMapping ties a SPIFFE identity (exact URI or URI prefix) to a Hoop
// agent, plus a set of groups that feed into RBAC on authentication.
//
// Exactly one of (SPIFFEID, SPIFFEPrefix) must be set, enforced by a CHECK
// constraint in the database. Similarly for (AgentID, AgentTemplate): exact
// match resolves to a specific agent_id; prefix matches render AgentTemplate
// against the captured suffix to produce an agent name to look up.
type AgentSPIFFEMapping struct {
	ID            string         `gorm:"column:id;primaryKey;default:uuid_generate_v4()"`
	OrgID         string         `gorm:"column:org_id"`
	TrustDomain   string         `gorm:"column:trust_domain"`
	SPIFFEID      *string        `gorm:"column:spiffe_id"`
	SPIFFEPrefix  *string        `gorm:"column:spiffe_prefix"`
	AgentID       *string        `gorm:"column:agent_id"`
	AgentTemplate *string        `gorm:"column:agent_template"`
	Groups        pq.StringArray `gorm:"column:groups;type:text[]"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	UpdatedAt     time.Time      `gorm:"column:updated_at"`
}

const tableAgentSPIFFEMappings = "private.agent_spiffe_mappings"

// ResolvedSPIFFEMapping is the resolved outcome of a SPIFFE-ID lookup: either
// a concrete Agent (when the mapping pointed at one) or, when the mapping used
// a prefix+template, the rendered agent name to be resolved by the caller.
type ResolvedSPIFFEMapping struct {
	Mapping   AgentSPIFFEMapping
	Agent     *Agent
	AgentName string
	Groups    []string
}

func ListAgentSPIFFEMappings(orgID string) ([]AgentSPIFFEMapping, error) {
	var items []AgentSPIFFEMapping
	err := DB.Table(tableAgentSPIFFEMappings).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

func GetAgentSPIFFEMapping(orgID, id string) (*AgentSPIFFEMapping, error) {
	var m AgentSPIFFEMapping
	err := DB.Table(tableAgentSPIFFEMappings).
		Where("org_id = ? AND id::TEXT = ?", orgID, id).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &m, err
}

func CreateAgentSPIFFEMapping(m *AgentSPIFFEMapping) error {
	if err := validateMappingShape(m); err != nil {
		return err
	}
	err := DB.Table(tableAgentSPIFFEMappings).Create(m).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrAlreadyExists
	}
	return err
}

func UpdateAgentSPIFFEMapping(m *AgentSPIFFEMapping) error {
	if err := validateMappingShape(m); err != nil {
		return err
	}
	res := DB.Table(tableAgentSPIFFEMappings).
		Where("org_id = ? AND id::TEXT = ?", m.OrgID, m.ID).
		Updates(map[string]any{
			"trust_domain":   m.TrustDomain,
			"spiffe_id":      m.SPIFFEID,
			"spiffe_prefix":  m.SPIFFEPrefix,
			"agent_id":       m.AgentID,
			"agent_template": m.AgentTemplate,
			"groups":         m.Groups,
			"updated_at":     time.Now().UTC(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func DeleteAgentSPIFFEMapping(orgID, id string) error {
	res := DB.Table(tableAgentSPIFFEMappings).
		Where("org_id = ? AND id::TEXT = ?", orgID, id).
		Delete(&AgentSPIFFEMapping{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveAgentFromSPIFFEID finds the best-matching mapping for the given
// SPIFFE ID in the given trust domain, scoped to orgs where trust_domain
// matches. Match priority:
//
//  1. exact match on spiffe_id
//  2. longest-prefix match on spiffe_prefix
//
// When a match is found, the agent is resolved either by agent_id (direct)
// or by rendering agent_template against the captured suffix and looking up
// by name in the same org.
//
// Note: this function does not filter by org up-front because the trust
// domain of an SVID does not carry org information. Multi-tenant deployments
// must configure distinct trust domains per org, or register non-overlapping
// SPIFFE-ID prefixes across orgs to avoid ambiguity.
func ResolveAgentFromSPIFFEID(trustDomain, spiffeID string) (*ResolvedSPIFFEMapping, error) {
	if trustDomain == "" || spiffeID == "" {
		return nil, ErrNotFound
	}

	// try exact match first
	var exact AgentSPIFFEMapping
	err := DB.Table(tableAgentSPIFFEMappings).
		Where("trust_domain = ? AND spiffe_id = ?", trustDomain, spiffeID).
		First(&exact).Error
	if err == nil {
		return resolveMapping(exact, spiffeID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// fall back to longest-prefix match
	var candidates []AgentSPIFFEMapping
	err = DB.Table(tableAgentSPIFFEMappings).
		Where("trust_domain = ? AND spiffe_prefix IS NOT NULL", trustDomain).
		Order("length(spiffe_prefix) DESC").
		Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if c.SPIFFEPrefix == nil {
			continue
		}
		if strings.HasPrefix(spiffeID, *c.SPIFFEPrefix) {
			return resolveMapping(c, spiffeID)
		}
	}
	return nil, ErrNotFound
}

func resolveMapping(m AgentSPIFFEMapping, spiffeID string) (*ResolvedSPIFFEMapping, error) {
	res := &ResolvedSPIFFEMapping{
		Mapping: m,
		Groups:  []string(m.Groups),
	}

	// direct agent_id mapping
	if m.AgentID != nil && *m.AgentID != "" {
		var agent Agent
		err := DB.Table("private.agents").
			Where("org_id = ? AND id::TEXT = ?", m.OrgID, *m.AgentID).
			First(&agent).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		res.Agent = &agent
		res.AgentName = agent.Name
		return res, nil
	}

	// template-based mapping: render against captured suffix
	if m.AgentTemplate == nil || *m.AgentTemplate == "" {
		return nil, fmt.Errorf("invalid mapping: both agent_id and agent_template are empty")
	}
	suffix := ""
	if m.SPIFFEPrefix != nil {
		suffix = strings.TrimPrefix(spiffeID, *m.SPIFFEPrefix)
	}
	tmpl, err := template.New("spiffe-agent").Parse(*m.AgentTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid agent_template: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"WorkloadIdentifier": suffix,
		"SPIFFEID":           spiffeID,
	}); err != nil {
		return nil, fmt.Errorf("failed rendering agent_template: %v", err)
	}
	res.AgentName = strings.TrimSpace(buf.String())
	if res.AgentName == "" {
		return nil, fmt.Errorf("agent_template rendered to empty string, spiffe_id=%q", spiffeID)
	}

	agent, err := GetAgentByNameOrID(m.OrgID, res.AgentName)
	if err != nil {
		return nil, err
	}
	res.Agent = agent
	return res, nil
}

func validateMappingShape(m *AgentSPIFFEMapping) error {
	if m.OrgID == "" {
		return fmt.Errorf("org_id is required")
	}
	if m.TrustDomain == "" {
		return fmt.Errorf("trust_domain is required")
	}
	hasID := m.SPIFFEID != nil && *m.SPIFFEID != ""
	hasPrefix := m.SPIFFEPrefix != nil && *m.SPIFFEPrefix != ""
	if hasID == hasPrefix {
		return fmt.Errorf("exactly one of spiffe_id or spiffe_prefix must be set")
	}
	if hasID && !strings.HasPrefix(*m.SPIFFEID, "spiffe://") {
		return fmt.Errorf("spiffe_id must start with spiffe://")
	}
	if hasPrefix && !strings.HasPrefix(*m.SPIFFEPrefix, "spiffe://") {
		return fmt.Errorf("spiffe_prefix must start with spiffe://")
	}
	hasAgentID := m.AgentID != nil && *m.AgentID != ""
	hasTemplate := m.AgentTemplate != nil && *m.AgentTemplate != ""
	if hasAgentID == hasTemplate {
		return fmt.Errorf("exactly one of agent_id or agent_template must be set")
	}
	return nil
}
