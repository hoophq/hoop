package apiroutes

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const enterpriseLicenseRequired = "this resource requires a valid enterprise license, " +
	"add one in Settings -> License or contact our support at https://help.hoop.dev"

// EnterpriseLicenseOnly refuses the request unless the organization holds a
// valid Enterprise license. Runs after AuthMiddleware, which loads the license
// into the request context. The same predicate /serverinfo reports as
// is_valid + type, so the API and the UI gate agree.
func EnterpriseLicenseOnly(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var data json.RawMessage
	if ctx.OrgLicenseData != nil {
		data = *ctx.OrgLicenseData
	}
	if !isValidEnterpriseLicense(data, appconfig.Get().ApiHostname()) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": enterpriseLicenseRequired})
		return
	}
	c.Next()
}

func isValidEnterpriseLicense(data json.RawMessage, hostname string) bool {
	if len(data) == 0 {
		return false
	}
	l, err := license.Parse(data, hostname)
	return err == nil && l.Payload.Type == license.EnterpriseType
}
