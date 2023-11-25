package pgusers

import (
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

func (u *user) ListGroups(ctx pgrest.Context) ([]string, error) {
	var userGroups []UserGroups
	err := pgrest.New("/user_groups_update?org_id=eq.%s&user_id=eq.%s", ctx.GetOrgID(), ctx.GetUserID()).
		List().
		DecodeInto(&userGroups)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	var groups []string
	for _, ug := range userGroups {
		groups = append(groups, ug.Name)
	}
	return groups, nil
}

// func (u *user) UpdateSlackID(ctx pgrest.OrgContext, slackID string) error {
// 	return nil
// }

func (u *user) FetchOneBySlackID(ctx pgrest.OrgContext, slackID string) (*types.User, error) {
	var usr pgrest.User
	err := pgrest.New("/users?select=*,orgs(id,name)&org_id=eq.%v&slack_id=eq.%v", ctx.GetOrgID(), slackID).
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
		Groups:  usr.Groups,
	}, nil
}
