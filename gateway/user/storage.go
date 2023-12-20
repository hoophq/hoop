package user

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
)

type Storage struct{}

func (s *Storage) FindById(ctx *Context, identifier string) (*Context, error) {
	u, err := pgusers.New().FetchOneBySubject(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return &Context{}, nil
	}
	return &Context{
			User: &User{u.Subject, u.Org.ID, u.Name, u.Email, StatusType(u.Status), u.SlackID, u.Groups},
			Org:  &Org{Id: u.Org.ID, Name: u.Org.Name}},
		nil
}

func (s *Storage) FindBySlackID(ctx *Org, slackID string) (*User, error) {
	u, err := pgusers.New().FetchOneBySlackID(ctx, slackID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	return &User{
		Id:      u.Id,
		Org:     u.Org,
		Name:    u.Name,
		Email:   u.Email,
		Status:  StatusType(u.Status),
		SlackID: u.SlackID,
		Groups:  u.Groups,
	}, nil
}

func (s *Storage) FindInvitedUser(email string) (*InvitedUser, error) {
	usr, err := pgusers.New().FetchUnverifiedUser(&Context{}, email)
	if err != nil {
		return nil, err
	}
	if usr == nil {
		return nil, nil
	}
	return &InvitedUser{
		Id:      usr.ID,
		Org:     usr.OrgID,
		Email:   usr.Email,
		Name:    usr.Name,
		SlackID: usr.SlackID,
		Groups:  usr.Groups,
	}, nil
}

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
	case *User:
		return 0, pgusers.New().Upsert(pgrest.User{
			// if the user doesn't exist, this id won't be used because it's not an uuid
			ID:      v.Id,
			OrgID:   v.Org,
			Subject: v.Id,
			Name:    v.Name,
			Email:   v.Email,
			Status:  string(v.Status),
			SlackID: v.SlackID,
			Groups:  v.Groups,
		})
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
	org, err := pgusers.New().FetchOrgByName(name)
	if err != nil {
		return nil, err
	}
	if org != nil {
		return &Org{Id: org.ID, Name: org.Name}, nil
	}
	return nil, nil

}

func (s *Storage) ListAllGroups(context *Context) ([]string, error) {
	return pgusers.New().ListAllGroups(context)
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
