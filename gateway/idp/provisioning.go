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
// It may not in a control plane whose groups are managed by the Slack import
// (ADR-0020): the two would overwrite each other, and the claim often names
// groups differently (Entra ID sends object ids, Slack user group handles).
// The gateway always syncs, as it did before.
//
// orgID empty means the login is creating the user, who lands in the default
// organization. In a control plane a failed check keeps the stored groups: a
// sign-in still succeeds, and a database hiccup cannot hand a provisioned
// org's groups to the claim.
func LoginSyncsGroups(orgID string) bool {
	if !appconfig.Get().IsControlPlane() {
		return true
	}
	if orgID == "" {
		org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
		if err != nil || org == nil {
			log.Warnf("failed obtaining the default organization to check group provisioning, reason=%v", err)
			return false
		}
		orgID = org.ID
	}
	managed, err := models.GroupsManagedByProvisioning(models.DB, orgID)
	if err != nil {
		log.With("org", orgID).Warnf("failed checking group provisioning, reason=%v", err)
		return false
	}
	return !managed
}
