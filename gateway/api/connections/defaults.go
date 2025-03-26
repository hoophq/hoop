package apiconnections

import (
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
)

var systemTags = map[string][]string{
	"hoop.dev/infrastructure.cloud":          {"aws", "gcp", "azure", "digitalocean", "cloudflare"},
	"hoop.dev/infrastructure.environment":    {"prod", "staging", "dev", "qa", "sandbox"},
	"hoop.dev/infrastructure.service-type":   {"database", "cache", "compute", "storage", "cdn"},
	"hoop.dev/infrastructure.backup-policy":  {"daily", "weekly", "monthly", "none"},
	"hoop.dev/organization.team":             {"backend", "frontend", "platform", "devops", "security", "data"},
	"hoop.dev/organization.department":       {"engineering", "marketing", "sales", "finance"},
	"hoop.dev/security.compliance":           {"sox", "hipaa", "gdpr", "pci", "iso27001"},
	"hoop.dev/security.access-level":         {"public", "private", "restricted", "confidential"},
	"hoop.dev/security.data-classification":  {"public", "private", "restricted", "confidential"},
	"hoop.dev/security.security-zone":        {"public", "private", "restricted", "confidential"},
	"hoop.dev/operations.monitoring":         {"basic", "enhanced", "full"},
	"hoop.dev/operations.sla":                {"99.9", "99.99", "99.999"},
	"hoop.dev/operations.maintenance-window": {"weekend", "business-hours", "24x7"},
	"hoop.dev/operations.priority":           {"p1", "p2", "p3", "p4"},
	"hoop.dev/operations.impact":             {"high", "medium", "low"},
	"hoop.dev/operations.criticality":        {"critical", "important", "normal"},
	"hoop.dev/business.customer-facing":      {"yes", "no"},
}

func DefaultConnectionTags(orgID string) (items []models.ConnectionTag) {
	for key, vals := range systemTags {
		for _, val := range vals {
			items = append(items, models.ConnectionTag{
				OrgID:     orgID,
				ID:        uuid.NewString(),
				Key:       key,
				Value:     val,
				UpdatedAt: time.Now().UTC(),
				CreatedAt: time.Now().UTC(),
			})
		}
	}
	return
}
