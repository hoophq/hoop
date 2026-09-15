package accessrequests

import (
	"strings"
	"testing"

	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

func validSidecarRule() openapi.AccessRequestRuleRequest {
	return openapi.AccessRequestRuleRequest{
		Name:                   "prod-approvals",
		AccessType:             models.AccessTypeSidecar,
		SidecarNames:           []string{"sidecar-prod"},
		ConnectionNames:        []string{},
		ApprovalRequiredGroups: []string{},
		ReviewersGroups:        []string{"admin", "approver"},
		ForceApprovalGroups:    []string{},
		MinApprovals:           ptr.Int(1),
	}
}

func TestValidateSidecarAccessRequestRuleBody(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *openapi.AccessRequestRuleRequest)
		wantErr string
	}{
		{name: "valid", mutate: func(*openapi.AccessRequestRuleRequest) {}},
		// Only the name, sidecar_names and connection_names are checked; every
		// other field is stored as sent.
		{name: "no reviewer settings", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ReviewersGroups, r.MinApprovals = nil, nil
		}},
		{name: "any other field", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ReviewersGroups, r.ForceApprovalGroups = []string{"dba"}, []string{"sre"}
			r.ApprovalRequiredGroups, r.SkipReviewGroups = []string{"developers"}, []string{"sre"}
			r.Attributes = []string{"production"}
			r.AccessMaxDuration = ptr.Int(3600)
		}},
		{name: "invalid name", wantErr: "name", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.Name = "prod/approvals"
		}},
		{name: "no sidecars", wantErr: "sidecar_names", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.SidecarNames = nil
		}},
		{name: "connections", wantErr: "connection_names", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ConnectionNames = []string{"pg-prod"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validSidecarRule()
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

// A connection rule never stores sidecars: nothing would read them.
func TestValidateAccessRequestRuleBodyRefusesSidecarNames(t *testing.T) {
	req := openapi.AccessRequestRuleRequest{
		Name:                   "prod-jit",
		AccessType:             models.AccessTypeJit,
		ConnectionNames:        []string{"pg-prod"},
		ApprovalRequiredGroups: []string{},
		ReviewersGroups:        []string{"sre"},
		ForceApprovalGroups:    []string{},
		MinApprovals:           ptr.Int(1),
		SidecarNames:           []string{"sidecar-prod"},
	}
	err := validateAccessRequestRuleBody(uuid.New(), &req, nil)
	if err == nil || !strings.Contains(err.Error(), "sidecar_names") {
		t.Fatalf("expected an error naming sidecar_names, got %v", err)
	}

	req.SidecarNames = nil
	if err := validateAccessRequestRuleBody(uuid.New(), &req, nil); err != nil {
		t.Fatalf("a connection rule without sidecars must stay valid, got %v", err)
	}
}
