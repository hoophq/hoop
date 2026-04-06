package apiroutes

import (
	"net/http"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/stretchr/testify/assert"
)

func TestIsGroupAllowed(t *testing.T) {
	for _, tt := range []struct {
		msg            string
		method         string
		excludeAuditor bool
		groups         []string
		routeRoles     []openapi.RoleType
		want           bool
	}{
		{
			msg:        "it should allow when the group is admin for any routes",
			method:     http.MethodGet,
			groups:     []string{types.GroupAdmin},
			routeRoles: []openapi.RoleType{openapi.RoleAuditorType},
			want:       true,
		},
		{
			msg:        "it should allow admin on POST routes",
			method:     http.MethodPost,
			groups:     []string{types.GroupAdmin},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType},
			want:       true,
		},
		{
			msg:            "it should allow admin even when auditor is excluded",
			method:         http.MethodGet,
			excludeAuditor: true,
			groups:         []string{types.GroupAdmin},
			routeRoles:     []openapi.RoleType{openapi.RoleAdminType},
			want:           true,
		},
		{
			msg:        "it should allow auditor on GET with auditor role",
			method:     http.MethodGet,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleAuditorType},
			want:       true,
		},
		{
			msg:        "it should allow auditor on GET with admin-only role",
			method:     http.MethodGet,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType},
			want:       true,
		},
		{
			msg:        "it should allow auditor on GET with no roles",
			method:     http.MethodGet,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{},
			want:       true,
		},
		{
			msg:        "it should allow auditor on HEAD requests",
			method:     http.MethodHead,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType},
			want:       true,
		},
		{
			msg:        "it should deny auditor on POST routes",
			method:     http.MethodPost,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{openapi.RoleStandardType},
			want:       false,
		},
		{
			msg:        "it should deny auditor on PUT routes",
			method:     http.MethodPut,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{},
			want:       false,
		},
		{
			msg:        "it should deny auditor on DELETE routes",
			method:     http.MethodDelete,
			groups:     []string{types.GroupAuditor},
			routeRoles: []openapi.RoleType{},
			want:       false,
		},
		{
			msg:            "it should deny auditor on GET when excluded",
			method:         http.MethodGet,
			excludeAuditor: true,
			groups:         []string{types.GroupAuditor},
			routeRoles:     []openapi.RoleType{openapi.RoleAdminType},
			want:           false,
		},
		{
			msg:        "it should allow a standard access if no role is present",
			method:     http.MethodGet,
			groups:     []string{},
			routeRoles: []openapi.RoleType{},
			want:       true,
		},
		{
			msg:        "it should allow when standard role is present",
			method:     http.MethodPost,
			groups:     []string{},
			routeRoles: []openapi.RoleType{openapi.RoleStandardType},
			want:       true,
		},
		{
			msg:        "it should deny if group is not allowed",
			method:     http.MethodGet,
			groups:     []string{"foo-group"},
			routeRoles: []openapi.RoleType{openapi.RoleAdminType, openapi.RoleAuditorType},
			want:       false,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := isGroupAllowed(tt.method, tt.excludeAuditor, tt.groups, tt.routeRoles...)
			assert.Equal(t, tt.want, got)
		})
	}
}
