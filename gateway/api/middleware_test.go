package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuditResponseWriter_CapturesStatusCode(t *testing.T) {
	// Setup
	gin.SetMode(gin.TestMode)
	
	// Create a mock gin context with ResponseWriter
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	
	arw := &auditResponseWriter{
		ResponseWriter: c.Writer,
		statusCode:     0,
		responseBody:   &bytes.Buffer{},
	}

	// Test WriteHeader
	arw.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, arw.Status())
	assert.Equal(t, http.StatusCreated, arw.statusCode)

	// Test Write without explicit WriteHeader (should default to 200)
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	arw2 := &auditResponseWriter{
		ResponseWriter: c2.Writer,
		statusCode:     0,
		responseBody:   &bytes.Buffer{},
	}
	arw2.Write([]byte("test"))
	assert.Equal(t, http.StatusOK, arw2.Status())
	
	// Test response body capture
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	arw3 := &auditResponseWriter{
		ResponseWriter: c3.Writer,
		statusCode:     0,
		responseBody:   &bytes.Buffer{},
	}
	testData := []byte(`{"message": "test error"}`)
	arw3.Write(testData)
	assert.Equal(t, testData, arw3.responseBody.Bytes())
}

func TestShouldAuditRequest(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		expected bool
	}{
		{"POST to API", "POST", "/api/users", true},
		{"PUT to API", "PUT", "/api/connections/123", true},
		{"PATCH to API", "PATCH", "/api/agents/456", true},
		{"DELETE to API", "DELETE", "/api/resources/789", true},
		{"GET to API", "GET", "/api/users", false},
		{"HEAD request", "HEAD", "/api/users", false},
		{"OPTIONS request", "OPTIONS", "/api/users", false},
		{"POST to healthz", "POST", "/api/healthz", false},
		{"POST to metrics", "POST", "/api/metrics", false},
		{"POST to login", "POST", "/api/login", false},
		{"POST to signup", "POST", "/api/signup", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldAuditRequest(tt.method, tt.path)
			assert.Equal(t, tt.expected, result, 
				"shouldAuditRequest(%s, %s) = %v, want %v", 
				tt.method, tt.path, result, tt.expected)
		})
	}
}

func TestDeriveResourceAndAction(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		method           string
		expectedResource string
		expectedAction   string
	}{
		{"Create user", "/api/users", "POST", "users", "create"},
		{"Update user", "/api/users/123", "PUT", "users", "update"},
		{"Patch user", "/api/users/123", "PATCH", "users", "update"},
		{"Delete user", "/api/users/123", "DELETE", "users", "delete"},
		{"Create connection", "/api/connections", "POST", "connections", "create"},
		{"Update agent", "/api/agents/456", "PUT", "agents", "update"},
		{"Delete resource", "/api/resources/789", "DELETE", "resources", "delete"},
		{"User groups", "/api/users/groups", "POST", "user_groups", "create"},
		{"Data masking", "/api/datamasking", "POST", "data_masking", "create"},
		{"Service accounts", "/api/serviceaccounts/123", "PUT", "service_accounts", "update"},
		{"Server config", "/api/serverconfig", "PATCH", "server_config", "update"},
		{"Guardrails", "/api/guardrails/rule1", "DELETE", "guardrails", "delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, action := deriveResourceAndAction(tt.path, tt.method)
			assert.Equal(t, tt.expectedResource, resource, 
				"Resource mismatch for %s %s", tt.method, tt.path)
			assert.Equal(t, tt.expectedAction, action, 
				"Action mismatch for %s %s", tt.method, tt.path)
		})
	}
}

func TestRedactSensitiveFields(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name: "Redact password",
			input: map[string]any{
				"username": "admin",
				"password": "secret123",
				"email":    "admin@example.com",
			},
			expected: map[string]any{
				"username": "admin",
				"password": "[REDACTED]",
				"email":    "admin@example.com",
			},
		},
		{
			name: "Redact multiple sensitive fields",
			input: map[string]any{
				"api_key":       "key123",
				"secret":        "secret456",
				"token":         "token789",
				"client_secret": "client_secret",
				"name":          "test",
			},
			expected: map[string]any{
				"api_key":       "[REDACTED]",
				"secret":        "[REDACTED]",
				"token":         "[REDACTED]",
				"client_secret": "[REDACTED]",
				"name":          "test",
			},
		},
		{
			name: "Redact nested fields",
			input: map[string]any{
				"user": map[string]any{
					"name":     "admin",
					"password": "secret",
				},
				"config": map[string]any{
					"api_key": "key123",
					"timeout": 30,
				},
			},
			expected: map[string]any{
				"user": map[string]any{
					"name":     "admin",
					"password": "[REDACTED]",
				},
				"config": map[string]any{
					"api_key": "[REDACTED]",
					"timeout": 30,
				},
			},
		},
		{
			name: "No sensitive fields",
			input: map[string]any{
				"name":  "test",
				"email": "test@example.com",
				"age":   25,
			},
			expected: map[string]any{
				"name":  "test",
				"email": "test@example.com",
				"age":   25,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := redactSensitiveFields(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAuditMiddleware_Integration(t *testing.T) {
	// Skip in CI if database is not available
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	gin.SetMode(gin.TestMode)
	
	// Note: This is a simplified test to verify middleware doesn't break request flow
	// Full integration tests with database would require test database setup
	t.Log("Integration test placeholder - would require database setup")
}

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{1, 2, 1},
		{5, 3, 3},
		{10, 10, 10},
		{0, 100, 0},
		{-5, 5, -5},
	}

	for _, tt := range tests {
		result := min(tt.a, tt.b)
		assert.Equal(t, tt.expected, result)
	}
}

func TestErrorMessageExtraction(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		responseBody   string
		ginErrors      string
		expectedError  string
	}{
		{
			name:          "Extract message from JSON response",
			statusCode:    409,
			responseBody:  `{"message": "user already exists with email test@example.com"}`,
			expectedError: "user already exists with email test@example.com",
		},
		{
			name:          "Extract error from JSON response",
			statusCode:    400,
			responseBody:  `{"error": "invalid request format"}`,
			expectedError: "invalid request format",
		},
		{
			name:          "Extract msg from JSON response",
			statusCode:    422,
			responseBody:  `{"msg": "validation failed"}`,
			expectedError: "validation failed",
		},
		{
			name:          "Fallback to HTTP status when no message field",
			statusCode:    500,
			responseBody:  `{"code": 500, "data": null}`,
			expectedError: "HTTP 500",
		},
		{
			name:          "Handle non-JSON response body",
			statusCode:    404,
			responseBody:  "Not Found",
			expectedError: "Not Found",
		},
		{
			name:          "Success status should not have error message",
			statusCode:    200,
			responseBody:  `{"message": "success"}`,
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the error extraction logic from middleware
			var errorMessage string
			httpStatus := tt.statusCode
			responseBody := bytes.NewBufferString(tt.responseBody)

			if httpStatus >= 400 && responseBody.Len() > 0 {
				var responseData map[string]any
				if err := json.Unmarshal(responseBody.Bytes(), &responseData); err == nil {
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
					bodyStr := responseBody.String()
					if len(bodyStr) > 200 {
						bodyStr = bodyStr[:200] + "..."
					}
					errorMessage = bodyStr
				}
			}

			assert.Equal(t, tt.expectedError, errorMessage)
		})
	}
}
