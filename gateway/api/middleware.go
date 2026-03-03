package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
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
		analytics.New().Track(ctx.UserID, eventName, properties)
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
		// Import audit package inline to avoid circular dependencies
		// Check if this request should be audited
		if !shouldAuditRequest(c.Request.Method, c.Request.URL.Path) {
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
			resourceType, action := deriveResourceAndAction(httpPath, httpMethod)

			// Collect error message if any
			var errorMessage string
			if len(c.Errors) > 0 {
				errorMessage = c.Errors.String()
			} else if httpStatus >= 400 && arw.responseBody != nil && arw.responseBody.Len() > 0 {
				// Try to extract error message from response body
				var responseData map[string]any
				if err := json.Unmarshal(arw.responseBody.Bytes(), &responseData); err == nil {
					// Try common error message fields
					if msg, ok := responseData["message"].(string); ok && msg != "" {
						errorMessage = msg
					} else if msg, ok := responseData["error"].(string); ok && msg != "" {
						errorMessage = msg
					} else if msg, ok := responseData["msg"].(string); ok && msg != "" {
						errorMessage = msg
					} else {
						errorMessage = fmt.Sprintf("HTTP %d", httpStatus)
					}
				} else {
					// If not JSON, use raw response body (truncated)
					bodyStr := arw.responseBody.String()
					if len(bodyStr) > 200 {
						bodyStr = bodyStr[:200] + "..."
					}
					errorMessage = bodyStr
				}
			}

			// Log to audit
			logAuditFromMiddleware(
				ctx,
				httpMethod,
				httpStatus,
				httpPath,
				clientIP,
				resourceType,
				action,
				requestBody,
				errorMessage,
			)
		}()
	}
}

// shouldAuditRequest determines if a request should be audited
func shouldAuditRequest(method, path string) bool {
	// Skip read operations
	if method == "GET" || method == "HEAD" || method == "OPTIONS" {
		return false
	}

	// Skip health check and metrics
	if strings.Contains(path, "/healthz") || 
	   strings.Contains(path, "/health") || 
	   strings.Contains(path, "/metrics") {
		return false
	}

	// Skip login/signup endpoints (these have their own audit)
	if strings.Contains(path, "/login") || strings.Contains(path, "/signup") {
		return false
	}

	// Audit all write operations
	return method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
}

// deriveResourceAndAction extracts resource type and action from path and method
func deriveResourceAndAction(path, method string) (string, string) {
	// Remove trailing slash
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(path, "/")

	// Default values
	resourceType := "unknown"
	action := "unknown"

	// Map method to action
	switch strings.ToUpper(method) {
	case "POST":
		action = "create"
	case "PUT", "PATCH":
		action = "update"
	case "DELETE":
		action = "delete"
	}

	// Extract resource from path (segment after /api/)
	for i, part := range parts {
		if part == "api" && i+1 < len(parts) {
			resourceSegment := parts[i+1]
			
			// Map to known resource types
			switch resourceSegment {
			case "users":
				if i+2 < len(parts) && parts[i+2] == "groups" {
					resourceType = "user_groups"
				} else {
					resourceType = "users"
				}
			case "connections":
				resourceType = "connections"
			case "agents":
				resourceType = "agents"
			case "resources":
				resourceType = "resources"
			case "guardrails":
				resourceType = "guardrails"
			case "datamasking", "data-masking":
				resourceType = "data_masking"
			case "serviceaccounts", "service-accounts":
				resourceType = "service_accounts"
			case "serverconfig", "server-config":
				resourceType = "server_config"
			case "authconfig", "auth-config":
				resourceType = "auth_config"
			case "orgkeys", "org-keys":
				resourceType = "org_keys"
			default:
				resourceType = resourceSegment
			}
			break
		}
	}

	return resourceType, action
}

// logAuditFromMiddleware writes the audit log entry
func logAuditFromMiddleware(
	ctx *storagev2.Context,
	httpMethod string,
	httpStatus int,
	httpPath string,
	clientIP string,
	resourceType string,
	action string,
	requestBody []byte,
	errorMessage string,
) {
	// Parse request body
	var payload map[string]any
	if len(requestBody) > 0 && len(requestBody) < 1024*1024 { // Limit to 1MB
		if err := json.Unmarshal(requestBody, &payload); err != nil {
			// If unmarshal fails, store as raw string
			payload = map[string]any{"_raw_body": string(requestBody[:min(len(requestBody), 1000)])}
		}
	}

	// Redact sensitive fields
	if payload != nil {
		payload = redactSensitiveFields(payload)
	}

	// Determine outcome
	outcome := httpStatus >= 200 && httpStatus < 400

	// Create audit log
	row := &models.SecurityAuditLog{
		OrgID:                  ctx.OrgID,
		ActorSubject:           ctx.UserID,
		ActorEmail:             ctx.UserEmail,
		ActorName:              ctx.UserName,
		CreatedAt:              time.Now().UTC(),
		ResourceType:           resourceType,
		Action:                 action,
		HttpMethod:             httpMethod,
		HttpStatus:             httpStatus,
		HttpPath:               httpPath,
		ClientIP:               clientIP,
		RequestPayloadRedacted: payload,
		Outcome:                outcome,
		ErrorMessage:           errorMessage,
	}

	if err := models.CreateSecurityAuditLog(row); err != nil {
		log.Errorf("failed to write audit log: %v", err)
	}
}

// redactSensitiveFields removes sensitive data from payload
func redactSensitiveFields(m map[string]any) map[string]any {
	redactKeys := map[string]struct{}{
		"password": {}, "hashed_password": {}, "client_secret": {},
		"secret": {}, "secrets": {}, "api_key": {}, "token": {}, "key": {},
		"env": {}, "envs": {}, "rollout_api_key": {}, "hosts_key": {},
	}

	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, ok := redactKeys[k]; ok {
			out[k] = "[REDACTED]"
			continue
		}
		
		switch val := v.(type) {
		case map[string]any:
			out[k] = redactSensitiveFields(val)
		case []map[string]any:
			items := make([]map[string]any, len(val))
			for i, item := range val {
				items[i] = redactSensitiveFields(item)
			}
			out[k] = items
		default:
			out[k] = v
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
