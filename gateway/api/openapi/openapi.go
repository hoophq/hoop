package openapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	_ "github.com/hoophq/hoop/gateway/api/openapi/autogen"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/swaggo/swag"
)

const instanceName = "swagger"

func Handler(c *gin.Context) {
	if swagger := swag.GetSwagger(instanceName); swagger != nil {
		if spec, ok := swagger.(*swag.Spec); ok {
			spec.Host = appconfig.Get().ApiHost()
			spec.BasePath = "/api"
			spec.Version = version.Get().Version
		}
		c.Header("Content-Type", "application/json; charset=utf-8")
		_, _ = c.Writer.Write([]byte(swagger.ReadDoc()))
		return
	}
	log.Warnf("unable to render swagger")
	c.JSON(http.StatusNoContent, nil)
}
