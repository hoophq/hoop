package userstorage

import (
	pglogin "github.com/runopsio/hoop/gateway/pgrest/login"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func UpdateLoginState(ctx *storagev2.Context, login *types.Login) error {
	return pglogin.New().Upsert(login)
}

func GetEntity(ctx *storagev2.Context, xtID string) (*types.User, error) {
	u, err := pgusers.New().FetchOneBySubject(ctx, xtID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	return &types.User{
		Id:      u.Subject,
		Org:     u.OrgID,
		Name:    u.Name,
		Email:   u.Email,
		Status:  types.UserStatusType(u.Status),
		SlackID: u.SlackID,
		Groups:  u.Groups}, nil
}

func FindByEmail(ctx *storagev2.Context, email string) (*types.User, error) {
	return pgusers.New().FetchOneByEmail(ctx, email)
}

func FindInvitedUser(ctx *storagev2.Context, email string) (*types.InvitedUser, error) {
	u, err := pgusers.New().FetchUnverifiedUser(ctx, email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	return &types.InvitedUser{
		ID:      u.ID,
		OrgID:   u.OrgID,
		Email:   u.Email,
		Name:    u.Name,
		SlackID: u.SlackID,
		Groups:  u.Groups}, nil
}

func UpdateInvitedUser(ctx *storagev2.Context, user *types.InvitedUser) error {
	return pgusers.New().UpsertUnverified(ctx, user)
}
