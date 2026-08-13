package apigdatamasking

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/models"
)

func TestValidateRulePayloadValidatesSupportedEntityTypeIdentifiers(t *testing.T) {
	tests := []struct {
		name    string
		entity  string
		wantErr bool
	}{
		{name: "canonical", entity: "DATE_TIME"},
		{name: "empty", entity: "", wantErr: true},
		{name: "whitespace", entity: " PERSON ", wantErr: true},
		{name: "lowercase", entity: "person", wantErr: true},
		{name: "punctuation", entity: "PERSON-NAME", wantErr: true},
		{name: "non ASCII", entity: "PÉRSON", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRulePayload(RulePayload{
				SupportedEntityTypes: []models.SupportedEntityTypesEntry{{
					Name:        "CUSTOM_SELECTION",
					EntityTypes: []string{tt.entity},
				}},
			})
			if tt.wantErr && err == nil {
				t.Fatal("expected invalid entity type to be rejected")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("canonical entity type rejected: %v", err)
			}
		})
	}
}

func TestValidateRulePayloadRejectsNonFiniteScoreThresholds(t *testing.T) {
	for _, threshold := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
		t.Run(fmt.Sprint(threshold), func(t *testing.T) {
			if err := ValidateRulePayload(RulePayload{ScoreThreshold: &threshold}); err == nil {
				t.Fatal("expected non-finite score threshold to be rejected")
			}
		})
	}
}

func TestParseRequestPayloadRejectsNonCanonicalSupportedEntityType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/datamasking-rules", strings.NewReader(`{
		"name": "invalid",
		"supported_entity_types": [{"name": "CUSTOM_SELECTION", "entity_types": ["person"]}]
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	if req, _ := parseRequestPayload(ctx); req != nil {
		t.Fatalf("non-canonical entity type parsed successfully: %#v", req)
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), "uppercase letters") {
		t.Fatalf("unexpected response body: %s", recorder.Body.String())
	}
}
