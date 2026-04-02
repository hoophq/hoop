package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func (a *Api) TrackRequest(eventName string) func(c *gin.Context) {
	return func(c *gin.Context) {
		ctx := storagev2.ParseContext(c)
		if ctx.UserEmail == "" || ctx.GetOrgID() == "" {
			c.Next()
			return
		}

		properties := map[string]any{
			"org-id":         ctx.OrgID,
			"auth-method":    appconfig.Get().AuthMethod(),
			"license-type":   ctx.GetLicenseType(),
			"content-length": c.Request.ContentLength,
			"user-agent":     apiutils.NormalizeUserAgent(c.Request.Header.Values),
			"api-hostname":   c.Request.Host,
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
		trackClient := analytics.New()
		defer trackClient.Close()

		trackClient.Track(ctx.UserID, eventName, properties)
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
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin, user-client")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func SecurityHeaderMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")

		c.Header("Content-Security-Policy",
			"default-src self; "+ // Allow everything from any domain
				"script-src * 'unsafe-inline' 'unsafe-eval'; "+ // Allow all scripts, including inline & eval()
				"script-src-elem * 'unsafe-inline'; "+ // Allow all script elements
				"style-src * 'unsafe-inline'; "+ // Allow all styles, including inline
				"font-src *; "+ // Allow fonts from any domain
				"connect-src *; "+ // Allow all API requests
				"img-src * data: blob:; "+ // Allow all images, including base64 & blobs
				"frame-src *; "+ // Allow embedding any frames
				"object-src *; "+ // Allow all objects (e.g., Flash, embeds)
				"worker-src *; ")

		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin")
		c.Header("Permissions-Policy", "geolocation=(),midi=(),sync-xhr=(),microphone=(),camera=(),magnetometer=(),gyroscope=(),fullscreen=(self),payment=()")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Next()
	}
}

type catchAll5xxResponseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (r *catchAll5xxResponseBodyWriter) Write(b []byte) (int, error) {
	const maxCapture = 2 * 1024 // 2KB
	if r.body.Len() < maxCapture {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func sentryCatchAll5xxMiddleware(c *gin.Context) {
	if enabled := appconfig.Get().AnalyticsTracking(); !enabled {
		c.Next()
		return
	}

	rbw := &catchAll5xxResponseBodyWriter{
		body:           &bytes.Buffer{},
		ResponseWriter: c.Writer,
	}
	c.Writer = rbw

	c.Next()
	status := c.Writer.Status()
	if status < 500 {
		return
	}

	hub := sentrygin.GetHubFromContext(c)
	if hub == nil {
		return
	}

	// Enrich scope with whatever context you need
	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("endpoint", fmt.Sprintf("%s %s", c.Request.Method, c.FullPath()))
		// Capture the first handler error if present
		if len(c.Errors) > 0 {
			_ = hub.CaptureException(c.Errors[0].Err)
			return
		}
	})
	c.Next()
}

// auditResponseWriter wraps gin.ResponseWriter to capture the HTTP status code and response body
type auditResponseWriter struct {
	gin.ResponseWriter
	statusCode   int
	responseBody *bytes.Buffer
}

func (w *auditResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	// Capture response body for error messages
	if w.responseBody != nil {
		w.responseBody.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *auditResponseWriter) Status() int {
	if w.statusCode == 0 {
		return http.StatusOK
	}
	return w.statusCode
}

// AuditMiddleware creates a middleware that automatically logs all write operations to the audit log.
// It captures HTTP request details (method, path, body, client IP) and response status.
func (a *Api) AuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if this request should be audited
		if !audit.ShouldAudit(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}

		// Capture request body for audit
		var requestBody []byte
		if c.Request.Body != nil {
			// Read body
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err == nil {
				requestBody = bodyBytes
				// Restore body for handler to read
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
		}

		// Wrap response writer to capture status code and response body
		arw := &auditResponseWriter{
			ResponseWriter: c.Writer,
			statusCode:     0,
			responseBody:   &bytes.Buffer{},
		}
		c.Writer = arw

		// Execute handler
		c.Next()

		// Log the audit entry after handler completes
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("audit middleware panic recovered: %v", r)
				}
			}()

			ctx := storagev2.ParseContext(c)
			if ctx == nil || ctx.OrgID == "" {
				return
			}

			// Get HTTP details
			httpMethod := c.Request.Method
			httpStatus := arw.Status()
			httpPath := c.Request.URL.Path
			clientIP := c.ClientIP()

			// Derive resource type and action from path
			resourceType, action := audit.DeriveResourceAndAction(httpPath, httpMethod)

			// Collect error message if any
			var errorMessage string
			if len(c.Errors) > 0 {
				errorMessage = c.Errors.String()
			} else if httpStatus >= 400 && arw.responseBody != nil && arw.responseBody.Len() > 0 {
				errorMessage = extractErrorMessage(httpStatus, arw.responseBody)
			}

			// Log to audit using the audit package function
			audit.LogFromMiddleware(
				ctx,
				httpMethod,
				httpStatus,
				httpPath,
				clientIP,
				audit.ResourceType(resourceType),
				audit.Action(action),
				requestBody,
				errorMessage,
			)
		}()
	}
}

// extractErrorMessage extracts a meaningful error message from the response body
func extractErrorMessage(httpStatus int, responseBody *bytes.Buffer) string {
	var responseData map[string]any
	if err := json.Unmarshal(responseBody.Bytes(), &responseData); err == nil {
		// Try common error message fields
		if msg, ok := responseData["message"].(string); ok && msg != "" {
			return msg
		} else if msg, ok := responseData["error"].(string); ok && msg != "" {
			return msg
		} else if msg, ok := responseData["msg"].(string); ok && msg != "" {
			return msg
		}
		return fmt.Sprintf("HTTP %d", httpStatus)
	}
	// If not JSON, use raw response body (truncated)
	bodyStr := responseBody.String()
	if len(bodyStr) > 200 {
		bodyStr = bodyStr[:200] + "..."
	}
	return bodyStr
}
