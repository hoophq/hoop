package pgusers

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type UserGroups struct {
	OrgID            string  `json:"org_id"`
	UserID           *string `json:"user_id"`
	ServiceAccountID *string `json:"service_account_id"`
	Name             string  `json:"name"`
}

type user struct{}

func New() *user { return &user{} }

func (u *user) UpsertUnverified(ctx pgrest.OrgContext, user *types.InvitedUser) error {
	return pgrest.New("/rpc/update_users?select=id,org_id,subject,email,name,verified,status,slack_id,created_at,updated_at,groups").
		RpcCreate(map[string]any{
			"id":       user.ID,
			"subject":  user.ID,
			"org_id":   ctx.GetOrgID(),
			"name":     user.Name,
			"email":    user.Email,
			"verified": false,
			"status":   "reviewing",
			"slack_id": user.SlackID,
			"groups":   user.Groups,
		}).Error()
}

func (u *user) Upsert(v pgrest.User) (err error) {
	var existentUsr pgrest.User
	if err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%s&subject=eq.%s", v.OrgID, v.Subject).
		FetchOne().
		DecodeInto(&existentUsr); err != nil && err != pgrest.ErrNotFound {
		return fmt.Errorf("failed fetching user: %v", err)
	}
	userID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(v.Subject)).String()
	if existentUsr.ID != "" {
		userID = existentUsr.ID
	}
	return pgrest.New("/rpc/update_users?select=id,org_id,subject,email,name,verified,status,slack_id,created_at,updated_at,groups").
		RpcCreate(map[string]any{
			"id":       userID,
			"subject":  v.Subject,
			"org_id":   v.OrgID,
			"name":     v.Name,
			"email":    v.Email,
			"verified": true,
			"status":   v.Status,
			"slack_id": v.SlackID,
			"groups":   v.Groups,
		}).Error()
}

func (u *user) FetchOneBySubject(ctx pgrest.OrgContext, subject string) (*pgrest.User, error) {
	path := fmt.Sprintf("/users?select=*,groups,orgs(id,name)&subject=eq.%v&verified=is.true", subject)
	orgID := ctx.GetOrgID()
	if orgID != "" {
		path = fmt.Sprintf("/users?select=*,groups,orgs(id,name)&subject=eq.%v&org_id=eq.%s&verified=is.true", subject, orgID)
	}
	var usr pgrest.User
	if err := pgrest.New(path).FetchOne().DecodeInto(&usr); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &usr, nil
}

// ListAllGroups of an organization
func (u *user) ListAllGroups(ctx pgrest.OrgContext) ([]string, error) {
	var userGroups []UserGroups
	err := pgrest.New("/user_groups?select=name&org_id=eq.%s", ctx.GetOrgID()).
		List().
		DecodeInto(&userGroups)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	dedupeGroups := map[string]string{}
	for _, ug := range userGroups {
		dedupeGroups[ug.Name] = ug.Name
	}
	var groups []string
	for groupName := range dedupeGroups {
		groups = append(groups, groupName)
	}
	return groups, nil
}

func (u *user) FetchAll(ctx pgrest.OrgContext) ([]pgrest.User, error) {
	var users []pgrest.User
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&verified=is.true", ctx.GetOrgID()).
		List().
		DecodeInto(&users)
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return users, nil
}

func (u *user) FetchUnverifiedUser(ctx pgrest.OrgContext, email string) (*pgrest.User, error) {
	path := fmt.Sprintf("/users?select=*,groups,orgs(id,name)&email=eq.%v&verified=is.false", email)
	orgID := ctx.GetOrgID()
	if orgID != "" {
		path = fmt.Sprintf("/users?select=*,groups,orgs(id,name)&org_id=eq.%s&email=eq.%v&verified=is.false",
			orgID, email)
	}
	var usr pgrest.User
	if err := pgrest.New(path).FetchOne().DecodeInto(&usr); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &usr, nil
}

func (u *user) FetchOneByEmail(ctx pgrest.OrgContext, email string) (*types.User, error) {
	var usr pgrest.User
	err := pgrest.New(fmt.Sprintf("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&email=eq.%v&verified=is.true", ctx.GetOrgID(), email)).
		FetchOne().
		DecodeInto(&usr)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &types.User{
		Id:      usr.ID,
		Org:     usr.OrgID,
		Name:    usr.Name,
		Email:   usr.Email,
		Status:  types.UserStatusType(usr.Status),
		SlackID: usr.SlackID,
		Groups:  usr.Groups}, nil
}

func (u *user) FetchOneBySlackID(ctx pgrest.OrgContext, slackID string) (*types.User, error) {
	var usr pgrest.User
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&slack_id=eq.%v&verified=is.true", ctx.GetOrgID(), slackID).
		FetchOne().
		DecodeInto(&usr)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &types.User{
		Id:      usr.Subject,
		Org:     usr.OrgID,
		Name:    usr.Name,
		Email:   usr.Email,
		Status:  types.UserStatusType(usr.Status),
		SlackID: usr.SlackID,
		Groups:  usr.Groups,
	}, nil
}

func (u *user) CreateOrg(id, name string) error {
	return pgrest.New("/orgs").Create(map[string]any{"id": id, "name": name}).Error()
}

func (u *user) FetchOrgByName(name string) (*pgrest.Org, error) {
	var org pgrest.Org
	err := pgrest.New("/orgs?name=eq.%v", name).
		FetchOne().
		DecodeInto(&org)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

func (u *user) FetchOrgByID(id string) (*pgrest.Org, error) {
	var org pgrest.Org
	err := pgrest.New("/orgs?id=eq.%v", id).
		FetchOne().
		DecodeInto(&org)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

func (u *user) FetchAllOrgs() (items []pgrest.Org, err error) {
	err = pgrest.New("/orgs").FetchAll().DecodeInto(&items)
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return
}
