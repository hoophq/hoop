package mcpserver

import (
	"testing"
)

func intPtr(v int) *int { return &v }

func validCreateInput() *accessRequestRulesCreateInput {
	return &accessRequestRulesCreateInput{
		Name:                   "default-rule",
		AccessType:             "jit",
		ConnectionNames:        []string{"pgdemo"},
		ApprovalRequiredGroups: []string{"developers"},
		ReviewersGroups:        []string{"sre"},
		MinApprovals:           intPtr(1),
	}
}

func TestValidateAccessRequestRuleInput(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*accessRequestRulesCreateInput)
		wantErr bool
	}{
		{
			name:    "valid input",
			mutate:  func(in *accessRequestRulesCreateInput) {},
			wantErr: false,
		},
		{
			name: "empty approval_required_groups is allowed (rule applies to all users)",
			mutate: func(in *accessRequestRulesCreateInput) {
				in.ApprovalRequiredGroups = nil
			},
			wantErr: false,
		},
		{
			name: "empty reviewers_groups is rejected",
			mutate: func(in *accessRequestRulesCreateInput) {
				in.ReviewersGroups = nil
			},
			wantErr: true,
		},
		{
			name: "missing min_approvals without all_groups_must_approve is rejected",
			mutate: func(in *accessRequestRulesCreateInput) {
				in.MinApprovals = nil
			},
			wantErr: true,
		},
		{
			name: "missing min_approvals with all_groups_must_approve is allowed",
			mutate: func(in *accessRequestRulesCreateInput) {
				in.MinApprovals = nil
				in.AllGroupsMustApprove = true
			},
			wantErr: false,
		},
		{
			name: "no connection names and no attributes is rejected",
			mutate: func(in *accessRequestRulesCreateInput) {
				in.ConnectionNames = nil
				in.Attributes = nil
			},
			wantErr: true,
		},
		{
			name: "invalid access type is rejected",
			mutate: func(in *accessRequestRulesCreateInput) {
				in.AccessType = "always"
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validCreateInput()
			tc.mutate(in)
			err := validateAccessRequestRuleInput(in)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateAccessRequestRuleInput() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAccessRequestRuleInputForUpdate(t *testing.T) {
	validUpdateInput := func() *accessRequestRulesUpdateInput {
		return &accessRequestRulesUpdateInput{
			Name:                   "default-rule",
			AccessType:             "command",
			ConnectionNames:        []string{"pgdemo"},
			ApprovalRequiredGroups: []string{"developers"},
			ReviewersGroups:        []string{"sre"},
			MinApprovals:           intPtr(1),
		}
	}
	tests := []struct {
		name    string
		mutate  func(*accessRequestRulesUpdateInput)
		wantErr bool
	}{
		{
			name:    "valid input",
			mutate:  func(in *accessRequestRulesUpdateInput) {},
			wantErr: false,
		},
		{
			name: "empty approval_required_groups is allowed (rule applies to all users)",
			mutate: func(in *accessRequestRulesUpdateInput) {
				in.ApprovalRequiredGroups = nil
			},
			wantErr: false,
		},
		{
			name: "empty reviewers_groups is rejected",
			mutate: func(in *accessRequestRulesUpdateInput) {
				in.ReviewersGroups = nil
			},
			wantErr: true,
		},
		{
			name: "missing min_approvals without all_groups_must_approve is rejected",
			mutate: func(in *accessRequestRulesUpdateInput) {
				in.MinApprovals = nil
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validUpdateInput()
			tc.mutate(in)
			err := validateAccessRequestRuleInputForUpdate(in)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateAccessRequestRuleInputForUpdate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
