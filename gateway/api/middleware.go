package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func (a *Api) TrackRequest(eventName string) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := storagev2.ParseContext(c)
		if ctx.UserEmail == "" || ctx.GetOrgID() == "" {
			c.Next()
			return
		}

		licenseType := license.OSSType
		if ctx.OrgLicenseData != nil && len(*ctx.OrgLicenseData) > 0 {
			var l license.License
			err := json.Unmarshal(*ctx.OrgLicenseData, &l)
			if err == nil {
				licenseType = l.Payload.Type
			}
		}

		properties := map[string]any{
			"host":           c.Request.Host,
			"auth-method":    appconfig.Get().AuthMethod(),
			"license-type":   licenseType,
			"content-length": c.Request.ContentLength,
			"api-url":        appconfig.Get().ApiURL(),
			"user-agent":     apiutils.NormalizeUserAgent(c.Request.Header.Values),
		}
		switch eventName {
		case analytics.EventCreateAgent:
			requestBody, _ := io.ReadAll(c.Request.Body)
			data := getBodyAsMap(requestBody)
			reCopyBody(requestBody, c)
			if agentMode, ok := data["mode"]; ok {
				properties["mode"] = fmt.Sprintf("%v", agentMode)
			}
		case analytics.EventUpdateConnection, analytics.EventCreateConnection:
			requestBody, _ := io.ReadAll(c.Request.Body)
			data := getBodyAsMap(requestBody)
			reCopyBody(requestBody, c)
			for key, val := range data {
				switch key {
				case "command":
					properties[key] = ""
					cmd, ok := val.([]any)
					if ok && len(cmd) > 0 {
						properties[key] = fmt.Sprintf("%v", cmd[0])
						continue
					}
					cmd2, ok := val.([]string)
					if ok && len(cmd2) > 0 {
						properties[key] = fmt.Sprintf("%v", cmd2[0])
					}
				case "type", "subtype":
					val := fmt.Sprintf("%v", val)
					// TODO; command must only have the first name of the command
					properties[key] = fmt.Sprintf("%v", val)
				}
			}
		case analytics.EventCreatePlugin, analytics.EventUpdatePlugin, analytics.EventUpdatePluginConfig:
			resourceName, ok := c.Params.Get("name")
			if !ok {
				requestBody, _ := io.ReadAll(c.Request.Body)
				data := getBodyAsMap(requestBody)
				reCopyBody(requestBody, c)
				resourceName = fmt.Sprintf("%v", data["name"])
			}
			if resourceName != "" {
				properties["plugin-name"] = resourceName
			}
		}
		analytics.New().Track(ctx.UserEmail, eventName, properties)
		c.Next()
	}
}

func reCopyBody(requestBody []byte, c *gin.Context) {
	if len(requestBody) == 0 {
		return
	}
	newBody := make([]byte, len(requestBody))
	_ = copy(newBody, requestBody)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(newBody))
}

func getBodyAsMap(data []byte) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func CORSMiddleware() gin.HandlerFunc {
	vs := version.Get()
	return func(c *gin.Context) {
		c.Writer.Header().Set("Server", fmt.Sprintf("hoopgateway/%s", vs.Version))
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin, x-backend-api, user-client")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
