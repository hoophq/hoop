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

// CreateDefaultOrganization creates a default organization if there isn't any.
// Having multiple organizations returns an error and a manual intervention is
// required
func CreateDefaultOrganization() error {
	orgList, err := New().FetchAllOrgs()
	if err != nil {
		return fmt.Errorf("failed fetching orgs, err=%v", err)
	}
	switch len(orgList) {
	case 0:
		if err := New().CreateOrg(uuid.NewString(), proto.DefaultOrgName); err != nil {
			return fmt.Errorf("failed creating the default organization, err=%v", err)
		}
	case 1: // noop
	default:
		return fmt.Errorf("found multiple organizations, cannot promote. orgs=%v", orgList)
	}
	return nil
}

func IsOrgMultiTenant() bool { return os.Getenv("ORG_MULTI_TENANT") == "true" }

func ContextLogger(c *gin.Context) *zap.SugaredLogger {
	obj, _ := c.Get(ContextLoggerKey)
	if logger := obj.(*zap.SugaredLogger); logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}
