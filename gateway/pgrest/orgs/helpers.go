package pgorgs

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/pgrest"
)

// CreateDefaultOrganization list all organizations and create a default
// if there is not any. Otherwise returns the ID of the first organization.
// In case there are more than one organization, returns an error.
func CreateDefaultOrganization(licenseDataJSON []byte) (pgrest.OrgContext, error) {
	orgList, err := New().FetchAllOrgs()
	if err != nil {
		return nil, fmt.Errorf("failed listing orgs, err=%v", err)
	}
	switch {
	case len(orgList) == 0:
		orgID := uuid.NewString()
		if err := New().CreateOrg(orgID, proto.DefaultOrgName, licenseDataJSON); err != nil {
			return nil, fmt.Errorf("failed creating the default organization, err=%v", err)
		}
		return pgrest.NewOrgContext(orgID), nil
	case len(orgList) == 1:
		return pgrest.NewOrgContext(orgList[0].ID), nil
	}
	return nil, fmt.Errorf("multiple organizations were found")
}
