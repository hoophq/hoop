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

func (c *orgContext) GetOrgID() string       { return c.orgID }
func (c *orgContext) GetLicenseName() string { return c.orgLicense }
func NewOrgContext(orgID string) OrgContext  { return &orgContext{orgID: orgID} }
func NewLicenseContext(orgID, orgLicense string) LicenseContext {
	return &orgContext{orgID, orgLicense}
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
