package pgusers

import "github.com/runopsio/hoop/gateway/pgrest"

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
