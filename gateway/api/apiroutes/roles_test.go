package apiroutes

import (
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
)

func TestIsGroupAllowed(t *testing.T) {
	for _, tt := range []struct {
		msg        string
		groups     []string
		routeRoles []openapi.RoleType
		want       bool
	}{
		{
			msg:        "it should allow when the group is admin for any routes",
			groups:     []string{types.GroupAdmin},
			routeRoles: []openapi.RoleType{openapi.RoleAuditorType},
			want:       true,
		},
		{
			msg:        "it should allow only when auditor role is present",
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleAuditorType},
			want:       true,
		},
		{
			msg:        "it should deny auditor on standard routes with no roles",
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{},
			want:       false,
		},
		{
			msg:        "it should deny auditor on admin-only routes",
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType},
			want:       false,
		},
		{
			msg:        "it should allow auditor on admin-and-auditor routes",
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType, openapi.RoleAuditorType},
			want:       true,
		},
		{
			msg:        "it should deny standard user on admin-and-auditor routes",
			groups:     []string{},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType, openapi.RoleAuditorType},
			want:       false,
		},
		{
			msg:        "it should allow a standard access if no role is present",
			groups:     []string{},
			routeRoles: []openapi.RoleType{},
			want:       true,
		},
		{
			msg:        "it should allow when standard role is present",
			groups:     []string{},
			routeRoles: []openapi.RoleType{openapi.RoleStandardType},
			want:       true,
		},
		{
			msg:        "it should deny if group is not allowed",
			groups:     []string{"foo-group"},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType, openapi.RoleAuditorType},
			want:       false,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := isGroupAllowed(tt.groups, tt.routeRoles...)
			assert.Equal(t, tt.want, got)
		})
	}

}
