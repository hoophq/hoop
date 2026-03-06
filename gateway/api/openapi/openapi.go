package openapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	_ "github.com/hoophq/hoop/gateway/api/openapi/autogen"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/swaggo/swag"
)

const instanceName = "swagger"

// Handler returns openapi v2 (a.k.a swagger) spec as JSON
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
	log.Warnf("unable to render openapi spec (v2)")
	c.JSON(http.StatusNoContent, nil)
}

// GenerateV3Spec converts spec v2 to openapi v3
func GenerateV3Spec() ([]byte, error) {
	if swagger := swag.GetSwagger(instanceName); swagger != nil {
		if spec, ok := swagger.(*swag.Spec); ok {
			spec.Host = appconfig.Get().ApiHost()
			spec.BasePath = "/api"
			spec.Version = version.Get().Version
		}
		return toV3([]byte(swagger.ReadDoc()))
	}
	return nil, fmt.Errorf("unable to render openapi spec (v3)")
}

// HandlerV3 returns openapi v3 spec as JSON
func HandlerV3(c *gin.Context) {
	v3Spec, err := GenerateV3Spec()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.Header("Content-Type", "application/json; charset=utf-8")
	if _, err := c.Writer.Write(v3Spec); err != nil {
		log.Errorf("failed to write v3 spec: %v", err)
		return
	}
}

func toV3(v2Spec []byte) ([]byte, error) {
	var doc2 openapi2.T
	if err := json.Unmarshal(v2Spec, &doc2); err != nil {
		return nil, fmt.Errorf("failed decoding v2 spec to json: %v", err)
	}
	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		return nil, fmt.Errorf("failed converting v2 spec to v3: %v", err)
	}
	doc3JSON, err := doc3.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed encoding v3 spec to json: %v", err)
	}
	return doc3JSON, nil
}
