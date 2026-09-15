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
		{name: "all groups must approve needs no minimum", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.AllGroupsMustApprove, r.MinApprovals = true, nil
		}},
		// A sidecar review reads none of these today. They are stored as sent,
		// so a later change can read them without refusing stored rules.
		{name: "any reviewer and force approval group", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ReviewersGroups, r.ForceApprovalGroups = []string{"dba"}, []string{"sre"}
		}},
		{name: "attributes", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.Attributes = []string{"production"}
		}},
		{name: "approval required groups", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ApprovalRequiredGroups = []string{"developers"}
		}},
		{name: "skip review groups", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.SkipReviewGroups = []string{"sre"}
		}},
		{name: "access window", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.AccessMaxDuration = ptr.Int(3600)
		}},
		{name: "no sidecars", wantErr: "sidecar_names", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.SidecarNames = nil
		}},
		{name: "connections", wantErr: "connection_names", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ConnectionNames = []string{"pg-prod"}
		}},
		{name: "no reviewers", wantErr: "reviewers_groups", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ReviewersGroups = nil
		}},
		{name: "no minimum", wantErr: "min_approvals", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.MinApprovals = nil
		}},
		{name: "skip and require review groups together", wantErr: "skip_review_groups", mutate: func(r *openapi.AccessRequestRuleRequest) {
			r.ApprovalRequiredGroups, r.SkipReviewGroups = []string{"developers"}, []string{"sre"}
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
