package openapi

import (
	"libhoop/log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/api/openapi/autogen"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/swaggo/swag"
)

const instanceName = "swagger"

func Handler(c *gin.Context) {
	autogen.SwaggerInfo.Host = appconfig.Get().ApiHostname()
	autogen.SwaggerInfo.BasePath = "/api"
	autogen.SwaggerInfo.Version = version.Get().Version
	if s := swag.GetSwagger(instanceName); s != nil {
		c.Header("Content-Type", "application/json; charset=utf-8")
		_, _ = c.Writer.Write([]byte(s.ReadDoc()))
		return
	}
	log.Warnf("unable to render swagger")
	c.JSON(http.StatusNoContent, nil)
}
