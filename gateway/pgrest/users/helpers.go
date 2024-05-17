package pgusers

import (
	"fmt"
	"os"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"go.uber.org/zap"
)

const (
	ContextLoggerKey = "context-logger"
	LicenseFreeType  = "free"
)

var licenseFreePlugins = []string{
	plugintypes.PluginAuditName,
	plugintypes.PluginEditorName,
	plugintypes.PluginIndexName,
}

func IsLicenseFreePlan(ctx pgrest.LicenseContext, pluginName string) bool {
	return ctx.GetLicenseName() == LicenseFreeType && !slices.Contains(licenseFreePlugins, pluginName)
}

// CreateDefaultOrganization list all organizations and create a default
// if there is not any. Otherwise returns the ID of the first organization.
// In case there are more than one organization, returns an error.
func CreateDefaultOrganization() (pgrest.OrgContext, error) {
	orgList, err := New().FetchAllOrgs()
	if err != nil {
		return nil, fmt.Errorf("failed listing orgs, err=%v", err)
	}
	switch {
	case len(orgList) == 0:
		orgID := uuid.NewString()
		if err := New().CreateOrg(orgID, proto.DefaultOrgName); err != nil {
			return nil, fmt.Errorf("failed creating the default organization, err=%v", err)
		}
		return pgrest.NewOrgContext(orgID), nil
	case len(orgList) == 1:
		return pgrest.NewOrgContext(orgList[0].ID), nil
	}
	return nil, fmt.Errorf("multiple organizations were found")

}

func IsOrgMultiTenant() bool { return os.Getenv("ORG_MULTI_TENANT") == "true" }

func ContextLogger(c *gin.Context) *zap.SugaredLogger {
	obj, _ := c.Get(ContextLoggerKey)
	if logger := obj.(*zap.SugaredLogger); logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}
