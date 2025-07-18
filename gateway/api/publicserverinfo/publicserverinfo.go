package apipublicserverinfo

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/idp"
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
		errMsg := fmt.Sprintf("failed to load server auth config: %v", err)
		log.Error(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": errMsg})
		return
	}
	publicServerInfo := openapi.PublicServerInfo{
		AuthMethod: string(authMethod),
	}
	c.JSON(http.StatusOK, publicServerInfo)
}
