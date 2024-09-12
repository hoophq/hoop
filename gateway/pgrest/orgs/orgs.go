package pgorgs

import (
	"encoding/json"
	"fmt"
	"libhoop/log"
	"net/url"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
)

var ErrOrgAlreadyExists = fmt.Errorf("organization already exists")

type org struct{}

func New() *org { return &org{} }

func decodeLicenseToMap(licenseDataJSON []byte) (map[string]any, error) {
	var out map[string]any
	return out, json.Unmarshal(licenseDataJSON, &out)
}

// CreateOrg creates an organization and store the license data if it's provided
func (o *org) CreateOrg(id, name string, licenseDataJSON []byte) error {
	requestPayload := map[string]any{"id": id, "name": name}
	if len(licenseDataJSON) > 0 {
		licenseData, err := decodeLicenseToMap(licenseDataJSON)
		if err != nil {
			return fmt.Errorf("unable to encode license data properly: %v", err)
		}
		requestPayload["license_data"] = licenseData
	}
	return pgrest.New("/orgs").Create(requestPayload).Error()
}

// CreateOrGetOrg creates an organization if it doesn't exist, otherwise
// it returns if the organization does not contain any users
func (o *org) CreateOrGetOrg(name string, licenseDataJSON []byte) (orgID string, err error) {
	org, _, err := o.FetchOrgByName(name)
	if err != nil {
		return "", err
	}
	if org != nil {
		var users []pgrest.User
		err = pgrest.New("/users?select=*,groups,orgs(id,name,license_data)&org_id=eq.%v", org.ID).
			List().
			DecodeInto(&users)
		if err != nil && err != pgrest.ErrNotFound {
			return "", fmt.Errorf("failed veryfing if org %s is empty, err=%v", org.ID, err)
		}
		// organization already exists and it's being used
		if len(users) > 0 {
			return "", ErrOrgAlreadyExists
		}
		return org.ID, nil
	}
	orgID = uuid.NewString()
	licenseData, _ := decodeLicenseToMap(licenseDataJSON)
	log.Debugf("licenseData: %v", licenseData)
	// if err != nil {
	// 	return "", fmt.Errorf("unable to encode license data properly: %v", err)
	// }
	return orgID, pgrest.New("/orgs").
		Create(map[string]any{"id": orgID, "name": name, "license_data": licenseData}).
		Error()
}

func (o *org) UpdateOrgLicense(ctx pgrest.OrgContext, licenseDataJSON []byte) error {
	if len(licenseDataJSON) == 0 {
		return fmt.Errorf("unable to update, license is empty")
	}
	licenseData, err := decodeLicenseToMap(licenseDataJSON)
	if err != nil {
		return fmt.Errorf("unable to encode license data properly: %v", err)
	}
	return pgrest.New("/orgs?id=eq.%s", ctx.GetOrgID()).
		Patch(map[string]any{"license_data": licenseData}).
		Error()
}

// FetchOrgByName returns an organization and the total number of users
func (o *org) FetchOrgByName(name string) (*pgrest.Org, int64, error) {
	var org pgrest.Org
	err := pgrest.New("/orgs?name=eq.%v", url.QueryEscape(name)).
		FetchOne().
		DecodeInto(&org)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	total := pgrest.New("/users?org_id=eq.%s", org.ID).ExactCount()
	return &org, total, nil
}

func (o *org) FetchOrgByContext(ctx pgrest.OrgContext) (*pgrest.Org, error) {
	var org pgrest.Org
	err := pgrest.New("/orgs?id=eq.%v", ctx.GetOrgID()).
		FetchOne().
		DecodeInto(&org)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &org, err
}

func (o *org) FetchAllOrgs() (items []pgrest.Org, err error) {
	err = pgrest.New("/orgs").FetchAll().DecodeInto(&items)
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return items, nil
}
