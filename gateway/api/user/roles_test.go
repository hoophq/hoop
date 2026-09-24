package userapi

import (
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

func TestRoleResolver(t *testing.T) {
	gateway := roleResolver{}
	controlPlane := roleResolver{controlPlane: true, reviewerGroups: map[string]bool{"dba-leads": true}}

	for _, tt := range []struct {
		name     string
		resolver roleResolver
		groups   []string
		want     openapi.RoleType
	}{
		{"gateway admin", gateway, []string{types.GroupAdmin, "dba-leads"}, openapi.RoleAdminType},
		{"gateway approver group", gateway, []string{types.GroupApprover}, openapi.RoleApproverType},
		{"gateway reviewer group is not a role", gateway, []string{"dba-leads"}, openapi.RoleStandardType},
		{"control plane admin", controlPlane, []string{types.GroupAdmin}, openapi.RoleAdminType},
		{"control plane reviewer group", controlPlane, []string{"sre", "dba-leads"}, openapi.RoleApproverType},
		// The reserved group means nothing on a control plane unless a rule
		// names it, as the rules created before ADR-0019 do.
		{"control plane approver group not named by a rule", controlPlane, []string{types.GroupApprover}, openapi.RoleStandardType},
		{"control plane no groups", controlPlane, nil, openapi.RoleStandardType},
		{"control plane after a failed read", roleResolver{controlPlane: true}, []string{"dba-leads"}, openapi.RoleStandardType},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resolver.role(openapi.User{Groups: tt.groups}); got != string(tt.want) {
				t.Errorf("role = %q, want %q", got, tt.want)
			}
		})
	}
}
