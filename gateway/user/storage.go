package user

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
)

type Storage struct{}

func (s *Storage) FindAll(context *Context) ([]User, error) {
	users, err := pgusers.New().FetchAll(context)
	if err != nil {
		return nil, err
	}
	var xtdbUsers []User
	for _, u := range users {
		xtdbUsers = append(xtdbUsers,
			User{u.Subject, u.OrgID, u.Name, u.Email, StatusType(u.Status), u.SlackID, u.Groups})
	}
	return xtdbUsers, nil
}

func (s *Storage) Persist(user any) (res int64, err error) {
	switch v := user.(type) {
	case *Org:
		return 0, pgusers.New().CreateOrg(v.Id, v.Name)
	default:
		return 0, fmt.Errorf("user.Persist: type (%T) not implemented", v)
	}
}

func (s *Storage) GetOrgNameByID(orgID string) (*Org, error) {
	org, err := pgusers.New().FetchOrgByID(orgID)
	if err != nil {
		return nil, err
	}
	if org == nil {
		return nil, nil
	}
	return &Org{Id: org.ID, Name: org.Name}, nil
}

func (s *Storage) GetOrgByName(name string) (*Org, error) {
	org, _, err := pgusers.New().FetchOrgByName(name)
	if err != nil {
		return nil, err
	}
	if org != nil {
		return &Org{Id: org.ID, Name: org.Name}, nil
	}
	return nil, nil

}

// Used when sending reports and by slack plugin
func (s *Storage) FindOrgs() ([]Org, error) {
	items, err := pgusers.New().FetchAllOrgs()
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	var orgList []Org
	for _, org := range items {
		orgList = append(orgList, Org{org.ID, org.Name, false})
	}
	return orgList, nil
}
