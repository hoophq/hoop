package accessrequests

import (
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
