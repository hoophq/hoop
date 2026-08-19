package apiorgs

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// customer_email is optional: the CLI always sends the key, empty when the flag
// is not set, and older clients omit it entirely. Both must bind.
func TestSignRequestCustomerEmailIsOptional(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"field absent", `{"license_type":"enterprise"}`, false},
		{"field empty", `{"license_type":"enterprise","customer_email":""}`, false},
		{"valid address", `{"license_type":"enterprise","customer_email":"user@hoop.dev"}`, false},
		{"malformed address", `{"license_type":"enterprise","customer_email":"not-an-email"}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")

			var req SignRequest
			err := c.ShouldBindJSON(&req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ShouldBindJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
