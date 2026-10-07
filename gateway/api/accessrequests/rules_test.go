package accessrequests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

func bindBody(t *testing.T, body string) (*openapi.AccessRequestRuleRequest, error) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/access-requests/rules", strings.NewReader(body))
	var req openapi.AccessRequestRuleRequest
	err := bindAccessRequestRule(c, &req)
	return &req, err
}

// A sidecar rule has no reason to send the connection fields the binding tags
// require; a connection rule still must.
func TestBindAccessRequestRuleByAccessType(t *testing.T) {
	req, err := bindBody(t, `{"name":"prod-approvals","access_type":"sidecar"}`)
	if err != nil {
		t.Fatalf("expected a sidecar rule without connection fields to bind, got %v", err)
	}
	if err := validateSidecarAccessRequestRuleBody(req); err != nil {
		t.Fatalf("expected the bound rule to pass sidecar validation, got %v", err)
	}
	if orEmpty(req.ReviewersGroups) == nil || orEmpty(req.ForceApprovalGroups) == nil {
		t.Fatal("expected omitted group lists to be stored as empty, not NULL")
	}

	if _, err := bindBody(t, `{"name":"prod-rule","access_type":"command"}`); err == nil {
		t.Fatal("expected a connection rule without its required fields to be refused")
	}
	if _, err := bindBody(t, `{"name":"prod-rule","access_type":"command","connection_names":["pg"],`+
		`"approval_required_groups":[],"reviewers_groups":["sre"],"force_approval_groups":[]}`); err != nil {
		t.Fatalf("expected a complete connection rule to bind, got %v", err)
	}
}

func TestValidateSidecarAccessRequestRuleBody(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *openapi.AccessRequestRuleRequest)
		wantErr string
	}{
		{name: "valid", mutate: func(*openapi.AccessRequestRuleRequest) {}},
		// Only the name, access_type and connection_names are checked; every
		// other field is stored as sent.
		{name: "any other field", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ReviewersGroups, r.ForceApprovalGroups = []string{"dba"}, []string{"sre"}
			r.ApprovalRequiredGroups, r.SkipReviewGroups = []string{"developers"}, []string{"sre"}
			r.Attributes = []string{"production"}
			r.AccessMaxDuration = ptr.Int(3600)
		}},
		{name: "invalid name", wantErr: "name", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.Name = "prod/approvals"
		}},
		// Only a sidecar rule takes this path.
		{name: "another access type", wantErr: "access_type must be 'sidecar'", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.AccessType = models.AccessTypeCommand
		}},
		{name: "connections", wantErr: "connection_names", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ConnectionNames = []string{"pg-prod"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := openapi.AccessRequestRuleRequest{Name: "prod-approvals", AccessType: models.AccessTypeSidecar}
			tt.mutate(&req)
			err := validateSidecarAccessRequestRuleBody(&req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected an error naming %s, got %v", tt.wantErr, err)
			}
		})
	}
}

// A gateway keeps its own access types, and its message never offers sidecar.
func TestValidateAccessRequestRuleBodyRefusesSidecarAccessType(t *testing.T) {
	req := openapi.AccessRequestRuleRequest{
		Name:                   "prod-rule",
		AccessType:             models.AccessTypeSidecar,
		ConnectionNames:        []string{"pg-prod"},
		ApprovalRequiredGroups: []string{},
		ReviewersGroups:        []string{"sre"},
		ForceApprovalGroups:    []string{},
		MinApprovals:           ptr.Int(1),
	}
	want := "access_type must be one of 'jit', 'command' or 'jit_command'"
	if err := validateAccessRequestRuleBody(uuid.New(), &req, nil); err == nil || err.Error() != want {
		t.Fatalf("expected %q, got %v", want, err)
	}
}

// A limit out of bounds answers 422 on both sidecar paths, before any write.
func TestSidecarRuleTTLBounds(t *testing.T) {
	for _, tt := range []struct {
		name               string
		pending, approval  *int
		wantErr            string
		wantPend, wantAppr *int
	}{
		{name: "absent"},
		{name: "clear", pending: ptr.Int(0), approval: ptr.Int(0), wantPend: ptr.Int(0), wantAppr: ptr.Int(0)},
		{name: "the bounds", pending: ptr.Int(60), approval: ptr.Int(604800), wantPend: ptr.Int(60), wantAppr: ptr.Int(604800)},
		{name: "pending too short", pending: ptr.Int(30), wantErr: "pending_ttl_sec"},
		{name: "pending negative", pending: ptr.Int(-60), wantErr: "pending_ttl_sec"},
		{name: "approval too long", approval: ptr.Int(604801), wantErr: "approval_ttl_sec"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := openapi.AccessRequestRuleRequest{
				Name: "prod-approvals", AccessType: models.AccessTypeSidecar,
				PendingTTLSec: tt.pending, ApprovalTTLSec: tt.approval,
			}
			pending, approval, err := sidecarRuleTTLs(&req)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !intPtrEqual(pending, tt.wantPend) || !intPtrEqual(approval, tt.wantAppr) {
					t.Fatalf("got %v/%v, want %v/%v", pending, approval, tt.wantPend, tt.wantAppr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want an error naming %s, got %v", tt.wantErr, err)
			}

			// The handlers answer 422 with that message and never reach the
			// database: the nil handle would panic.
			stored := &models.AccessRequestRule{Name: "prod-approvals", AccessType: models.AccessTypeSidecar, PendingTTLSec: ptr.Int(900)}
			for name, run := range map[string]func(c *gin.Context){
				"create": func(c *gin.Context) { createSidecarAccessRequestRule(c, nil, uuid.New(), &req) },
				"update": func(c *gin.Context) { updateSidecarAccessRequestRule(c, nil, uuid.New(), stored, &req) },
			} {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				run(c)
				if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), tt.wantErr) {
					t.Errorf("%s: got %d %s, want 422 naming %s", name, rec.Code, rec.Body, tt.wantErr)
				}
			}
			if stored.PendingTTLSec == nil || *stored.PendingTTLSec != 900 {
				t.Errorf("a refused update changed the loaded rule: %v", stored.PendingTTLSec)
			}
		})
	}
}

// A gateway rule never shows a limit, even one forced onto the row.
func TestToAccessRequestRuleOpenApiShowsTTLsOnlyOnSidecarRules(t *testing.T) {
	keys := func(rule *models.AccessRequestRule) map[string]any {
		t.Helper()
		raw, err := json.Marshal(toAccessRequestRuleOpenApi(rule))
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, accessType := range []string{models.AccessTypeJit, models.AccessTypeCommand, models.AccessTypeJitCommand} {
		out := keys(&models.AccessRequestRule{AccessType: accessType, PendingTTLSec: ptr.Int(900), ApprovalTTLSec: ptr.Int(600)})
		for _, k := range []string{"pending_ttl_sec", "approval_ttl_sec"} {
			if _, ok := out[k]; ok {
				t.Errorf("%s rule: %s present", accessType, k)
			}
		}
	}

	out := keys(&models.AccessRequestRule{AccessType: models.AccessTypeSidecar, PendingTTLSec: ptr.Int(900), ApprovalTTLSec: ptr.Int(600)})
	if out["pending_ttl_sec"] != float64(900) || out["approval_ttl_sec"] != float64(600) {
		t.Errorf("sidecar rule: got %v/%v, want 900/600", out["pending_ttl_sec"], out["approval_ttl_sec"])
	}
	out = keys(&models.AccessRequestRule{AccessType: models.AccessTypeSidecar})
	for _, k := range []string{"pending_ttl_sec", "approval_ttl_sec"} {
		if _, ok := out[k]; ok {
			t.Errorf("sidecar rule with no limit: %s present", k)
		}
	}
}
