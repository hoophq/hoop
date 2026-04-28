package apipublicserverinfo

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
)

// GetPublicServerInfo
//
//	@Summary		Get Public Server Info
//	@Description	Get public server information
//	@Tags			Server Management
//	@Produce		json
//	@Success		200	{object}	openapi.PublicServerInfo
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/publicserverinfo [get]
func Get(c *gin.Context) {
	_, authMethod, err := idp.LoadServerAuthConfig()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed to load server auth config: %v", err)
		return
	}

	setupRequired := false
	if !appconfig.Get().OrgMultitenant() {
		org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
		if err == nil {
			setupRequired = org.TotalUsers == 0
		}
	}

	c.JSON(http.StatusOK, openapi.PublicServerInfo{
		AuthMethod:    string(authMethod),
		SetupRequired: setupRequired,
	})
}
