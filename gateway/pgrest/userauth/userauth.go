package pguserauth

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
)

type userAuth struct{}

func New() *userAuth { return &userAuth{} }

// FetchUserContext fetches the user context based on the subject, which is usually
// the OIDC subject. In case the user doesn't exists, try to load a service account.
func (u *userAuth) FetchUserContext(subject string) (*Context, error) {
	usr, err := fetchOneBySubject(subject)
	if err != nil {
		return &Context{}, fmt.Errorf("failed fetching user for subject %s, %v", subject, err)
	}
	if usr == nil {
		sa, err := fetchServiceAccount(subject)
		if err != nil {
			return &Context{}, fmt.Errorf("failed fetching service account for subject %s, %v", subject, err)
		}
		if sa == nil {
			return &Context{}, nil
		}
		return &Context{
			OrgID:          sa.OrgID,
			OrgName:        sa.Org.Name,    // TODO: propagate org name
			OrgLicense:     sa.Org.License, // deprecated in flavor of OrgLicenseData
			OrgLicenseData: sa.Org.LicenseData,
			UserUUID:       sa.ID,
			UserSubject:    sa.Subject,
			UserName:       sa.Name,
			UserEmail:      sa.Subject,
			UserStatus:     sa.Status,
			UserPicture:    "",
			UserGroups:     sa.Groups,
		}, nil
	}

	return &Context{
		OrgID:          usr.OrgID,
		OrgName:        usr.Org.Name,
		OrgLicense:     usr.Org.License, // deprecated in flavor of OrgLicenseData
		OrgLicenseData: usr.Org.LicenseData,
		UserUUID:       usr.ID,
		UserSubject:    usr.Subject,
		UserEmail:      usr.Email,
		UserName:       usr.Name,
		UserStatus:     usr.Status,
		UserSlackID:    usr.SlackID,
		UserPicture:    usr.Picture,
		UserGroups:     usr.Groups,
	}, nil
}

func fetchOneBySubject(subject string) (*pgrest.User, error) {
	path := fmt.Sprintf("/users?select=*,groups,orgs(id,name,license,license_data)&subject=eq.%v", subject)
	var usr pgrest.User
	if err := pgrest.New(path).FetchOne().DecodeInto(&usr); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &usr, nil
}

func fetchServiceAccount(subject string) (*pgrest.ServiceAccount, error) {
	saID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("serviceaccount/%s", subject))).String()
	var sa pgrest.ServiceAccount
	err := pgrest.New("/serviceaccounts?select=*,groups,orgs(id,name,license,license_data)&id=eq.%s", saID).
		FetchOne().
		DecodeInto(&sa)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &sa, nil
}
