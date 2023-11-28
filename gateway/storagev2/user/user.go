package userstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pglogin "github.com/runopsio/hoop/gateway/pgrest/login"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func UpdateLoginState(ctx *storagev2.Context, login *types.Login) error {
	if pgrest.Rollout {
		return pglogin.New().Upsert(login)
	}
	_, err := ctx.Put(login)
	return err
}

func GetEntity(ctx *storagev2.Context, xtID string) (*types.User, error) {
	if pgrest.Rollout {
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
	data, err := ctx.GetEntity(xtID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var obj types.User
	return &obj, edn.Unmarshal(data, &obj)
}

func FindByEmail(ctx *storagev2.Context, email string) (*types.User, error) {
	if pgrest.Rollout {
		return pgusers.New().FetchOneByEmail(ctx, email)
	}

	ednQuery := fmt.Sprintf(`{:query {
		:find [(pull ?u [*])] 
		:in [orgid email]
		:where [[?u :user/org orgid]
				[?u :user/email email]]}
		:in-args [%q %q]}`, ctx.OrgID, email)
	data, err := ctx.Query(ednQuery)
	if err != nil {
		return nil, err
	}
	var user [][]types.User
	if err := edn.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	if len(user) == 0 {
		return nil, nil
	}
	return &user[0][0], nil
}

func FindInvitedUser(ctx *storagev2.Context, email string) (*types.InvitedUser, error) {
	if pgrest.Rollout {
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

	ednQuery := fmt.Sprintf(`{:query {
		:find [(pull ?u [*])] 
		:in [orgid email]
		:where [[?u :invited-user/org orgid]
				[?u :invited-user/email email]]}
		:in-args [%q %q]}`, ctx.OrgID, email)

	data, err := ctx.Query(ednQuery)
	if err != nil {
		return nil, err
	}

	var invitedUser [][]types.InvitedUser
	if err := edn.Unmarshal(data, &invitedUser); err != nil {
		return nil, err
	}

	if len(invitedUser) == 0 {
		return nil, nil
	}

	return &invitedUser[0][0], nil
}

func UpdateInvitedUser(ctx *storagev2.Context, user *types.InvitedUser) error {
	if pgrest.Rollout {
		return pgusers.New().UpsertUnverified(ctx, user)
	}

	_, err := ctx.Put(user)
	return err
}
