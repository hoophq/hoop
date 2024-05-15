package pgrest

import (
	"slices"

	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

var EmptyContext = NewOrgContext("")

type orgContext struct {
	orgID      string
	orgLicense string
}

type auditContext struct {
	orgID     string
	eventName string
	userEmail string
	metadata  map[string]any
}

func (c *orgContext) GetOrgID() string       { return c.orgID }
func (c *orgContext) GetLicenseName() string { return c.orgLicense }
func NewOrgContext(orgID string) OrgContext  { return &orgContext{orgID: orgID} }
func NewLicenseContext(orgID, orgLicense string) LicenseContext {
	return &orgContext{orgID, orgLicense}
}

func NewAuditContext(orgID, eventName, userEmail string) *auditContext {
	return &auditContext{
		orgID:     orgID,
		eventName: eventName,
		userEmail: userEmail,
		metadata:  nil,
	}
}
func (c *auditContext) GetOrgID() string            { return c.orgID }
func (c *auditContext) GetUserEmail() string        { return c.userEmail }
func (c *auditContext) GetEventName() string        { return c.eventName }
func (c *auditContext) GetMetadata() map[string]any { return c.metadata }
func (c *auditContext) WithMetadata(v map[string]any) *auditContext {
	c.metadata = v
	return c
}

const LicenseFreeType string = "free"

var licenseFreePlugins = []string{
	plugintypes.PluginAuditName,
	plugintypes.PluginEditorName,
	plugintypes.PluginIndexName,
}

// IsValidLicense check if the pluginName match with organization license.
//
// free license is valid when the plugin name is audit, indexer or editor
func IsValidLicense(ctx LicenseContext, pluginName string) (isValid bool) {
	switch ctx.GetLicenseName() {
	case LicenseFreeType:
		return slices.Contains(licenseFreePlugins, pluginName)
	default:
		return true
	}
}
