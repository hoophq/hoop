package pgusers

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

var ErrOrgAlreadyExists = fmt.Errorf("organization already exists")

type UserGroups struct {
	OrgID            string  `json:"org_id"`
	UserID           *string `json:"user_id"`
	ServiceAccountID *string `json:"service_account_id"`
	Name             string  `json:"name"`
}

type user struct{}

func New() *user { return &user{} }

func (u *user) Upsert(v pgrest.User) (err error) {
	return pgrest.New("/rpc/update_users?select=id,org_id,subject,email,name,verified,status,slack_id,created_at,updated_at,groups").
		RpcCreate(map[string]any{
			"id":       v.ID,
			"subject":  v.Subject,
			"org_id":   v.OrgID,
			"name":     v.Name,
			"email":    v.Email,
			"verified": v.Verified,
			"status":   v.Status,
			"slack_id": v.SlackID,
			"groups":   v.Groups,
		}).Error()
}

func (u *user) FetchOneBySubject(ctx pgrest.OrgContext, subject string) (*pgrest.User, error) {
	path := fmt.Sprintf("/users?select=*,groups,orgs(id,name)&subject=eq.%v&org_id=eq.%s",
		subject, ctx.GetOrgID())
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
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v", ctx.GetOrgID()).
		List().
		DecodeInto(&users)
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return users, nil
}

func (u *user) FetchUnverifiedUser(ctx pgrest.OrgContext, email string) (*pgrest.User, error) {
	path := fmt.Sprintf("/users?select=*,groups,orgs(id,name)&subject=eq.%s&verified=is.false", email)
	orgID := ctx.GetOrgID()
	if orgID != "" {
		path = fmt.Sprintf("/users?select=*,groups,orgs(id,name)&org_id=eq.%s&subject=eq.%s&verified=is.false",
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

func (u *user) FetchOneByEmail(ctx pgrest.OrgContext, email string) (*pgrest.User, error) {
	var usr pgrest.User
	err := pgrest.New(fmt.Sprintf("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&email=eq.%v", ctx.GetOrgID(), email)).
		FetchOne().
		DecodeInto(&usr)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &usr, nil
}

func (u *user) FetchOneBySlackID(ctx pgrest.OrgContext, slackID string) (*types.User, error) {
	var usr pgrest.User
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&slack_id=eq.%v&verified=is.true&status=eq.active",
		ctx.GetOrgID(), slackID).
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

// CreateOrGetOrg creates an organization if it doesn't exist, otherwise
// it returns if the organization does not contain any users
func (u *user) CreateOrGetOrg(name string) (orgID string, err error) {
	org, _, err := u.FetchOrgByName(name)
	if err != nil {
		return "", err
	}
	if org != nil {
		var users []pgrest.User
		err = pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v", org.ID).
			List().
			DecodeInto(&users)
		if err != nil {
			return "", fmt.Errorf("failed veryfing if org %s is empty, err=%v", org.ID, err)
		}
		// organization already exists and it's being used
		if len(users) > 0 {
			return "", ErrOrgAlreadyExists
		}
		return org.ID, nil
	}
	orgID = uuid.NewString()
	return orgID, pgrest.New("/orgs").Create(map[string]any{"id": orgID, "name": name}).Error()
}

// FetchOrgByName returns an organization and the total number of users
func (u *user) FetchOrgByName(name string) (*pgrest.Org, int64, error) {
	var org pgrest.Org
	err := pgrest.New("/orgs?name=eq.%v", name).
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

func (u *user) Delete(ctx pgrest.OrgContext, subject string) error {
	orgID := ctx.GetOrgID()
	if orgID == "" || subject == "" {
		return fmt.Errorf("missing subject and organization attributes")
	}
	return pgrest.New("/users?org_id=eq.%s&subject=eq.%s", orgID, subject).Delete().Error()
}

type orgCtx struct{ OrgID string }

func (c orgCtx) GetOrgID() string { return c.OrgID }
