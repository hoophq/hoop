package pgusers

import (
	"fmt"
	"net/url"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/pgrest"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

var ErrOrgAlreadyExists = fmt.Errorf("organization already exists")

type UserGroups struct {
	OrgID            string  `json:"org_id"`
	UserID           *string `json:"user_id"`
	ServiceAccountID *string `json:"service_account_id"`
	Name             string  `json:"name"`
}

type user struct{}

func GetOneByEmail(email string) (*pgrest.User, error) {
	var usr pgrest.User
	err := pgrest.New("/users?email=eq.%v", url.QueryEscape(email)).
		FetchOne().
		DecodeInto(&usr)
	if err != nil {
		if err == pgrest.ErrNotFound {
			log.Debugf("user not found")
			return nil, nil
		}
		return nil, err
	}
	return &usr, nil
}

func New() *user { return &user{} }

func (u *user) Upsert(v pgrest.User) (err error) {
	return pgrest.New("/rpc/update_users?select=id,org_id,subject,email,password,name,picture,verified,status,slack_id,created_at,updated_at,groups").
		RpcCreate(map[string]any{
			"id":       v.ID,
			"subject":  v.Subject,
			"org_id":   v.OrgID,
			"name":     v.Name,
			"picture":  v.Picture,
			"email":    v.Email,
			"password": v.Password,
			"verified": v.Verified,
			"status":   v.Status,
			"slack_id": v.SlackID,
			"groups":   v.Groups,
		}).Error()
}

func (u *user) FetchOneBySubject(ctx pgrest.OrgContext, subject string) (*pgrest.User, error) {
	var usr pgrest.User
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&subject=eq.%v&org_id=eq.%s",
		url.QueryEscape(subject), ctx.GetOrgID()).
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
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&order=email.asc", ctx.GetOrgID()).
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
	err := pgrest.New("/users?select=*,groups,orgs(id,name)&org_id=eq.%v&email=eq.%v", ctx.GetOrgID(), url.QueryEscape(email)).
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

func (u *user) Delete(ctx pgrest.OrgContext, subject string) error {
	orgID := ctx.GetOrgID()
	if orgID == "" || subject == "" {
		return fmt.Errorf("missing subject and organization attributes")
	}
	return pgrest.New("/users?org_id=eq.%s&subject=eq.%s", orgID, subject).Delete().Error()
}
