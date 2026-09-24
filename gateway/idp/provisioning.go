package idp

import (
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
)

// LoginSyncsGroups reports whether a login may rewrite the user's groups from
// the groups claim of their token.
//
// It may not in a control plane whose groups are provisioned by SCIM or a
// directory sync (ADR-0019): the two would overwrite each other, and the claim
// often names groups differently (Entra ID sends object ids, SCIM display
// names). The gateway always syncs, as it did before.
//
// orgID empty means the login is creating the user, who lands in the default
// organization. A failed check keeps the login's usual behavior rather than
// failing a sign-in over it.
func LoginSyncsGroups(orgID string) bool {
	if !appconfig.Get().IsControlPlane() {
		return true
	}
	if orgID == "" {
		org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
		if err != nil || org == nil {
			log.Warnf("failed obtaining the default organization to check group provisioning, reason=%v", err)
			return true
		}
		orgID = org.ID
	}
	managed, err := models.GroupsManagedByProvisioning(models.DB, orgID)
	if err != nil {
		log.With("org", orgID).Warnf("failed checking group provisioning, reason=%v", err)
		return true
	}
	return !managed
}
