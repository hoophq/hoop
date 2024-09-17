package apipublicserverinfo

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
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
	publicServerInfo := openapi.PublicServerInfo{
		AuthMethod: appconfig.Get().AuthMethod(),
	}
	c.PureJSON(http.StatusOK, publicServerInfo)
}
