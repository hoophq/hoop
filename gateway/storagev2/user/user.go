package userstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pglogin "github.com/runopsio/hoop/gateway/pgrest/login"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func UpdateLoginState(ctx *storagev2.Context, login *types.Login) error {
	if pgrest.WithPostgres(ctx) {
		return pglogin.New().Upsert(login)
	}
	_, err := ctx.Put(login)
	return err
}

func GetEntity(ctx *storagev2.Context, subject string) (*types.User, error) {
	var u pgrest.User
	err := pgrest.New(fmt.Sprintf("/users?select=*,org(id,name)&org_id=eq.%v&subject=eq.%v&verified=is.true", ctx.OrgID, subject)).
		FetchOne().
		DecodeInto(&u)
	switch err {
	case pgrest.ErrNotFound:
		return nil, nil
	case nil:
		return &types.User{u.ID, u.OrgID, u.Name, u.Email, types.UserStatusType(u.Status), u.SlackID, u.Groups}, nil
	default:
		return nil, err
	}

	// data, err := ctx.GetEntity(xtID)
	// if err != nil {
	// 	return nil, err
	// }
	// if data == nil {
	// 	return nil, nil
	// }
	// var obj types.User
	// return &obj, edn.Unmarshal(data, &obj)
}

func FindByEmail(ctx *storagev2.Context, email string) (*types.User, error) {
	var u pgrest.User
	err := pgrest.New(fmt.Sprintf("/users?select=*,org(id,name)&org_id=eq.%v&email=eq.%v&verified=is.true", ctx.OrgID, email)).
		FetchOne().
		DecodeInto(&u)
	switch err {
	case pgrest.ErrNotFound:
		return nil, nil
	case nil:
		return &types.User{u.ID, u.OrgID, u.Name, u.Email, types.UserStatusType(u.Status), u.SlackID, u.Groups}, nil
	default:
		return nil, err
	}

	// ednQuery := fmt.Sprintf(`{:query {
	// 	:find [(pull ?u [*])]
	// 	:in [orgid email]
	// 	:where [[?u :user/org orgid]
	// 			[?u :user/email email]]}
	// 	:in-args [%q %q]}`, ctx.OrgID, email)
	// data, err := ctx.Query(ednQuery)
	// if err != nil {
	// 	return nil, err
	// }
	// var user [][]types.User
	// if err := edn.Unmarshal(data, &user); err != nil {
	// 	return nil, err
	// }
	// if len(user) == 0 {
	// 	return nil, nil
	// }
	// return &user[0][0], nil
}

func FindInvitedUser(ctx *storagev2.Context, email string) (*types.InvitedUser, error) {
	client := pgrest.New(fmt.Sprintf("/users?select=*,org(id,name)&org_id=eq.%v&email=eq.%v&verified=is.false", ctx.OrgID, email))
	var u pgrest.User
	if err := client.FetchOne().DecodeInto(&u); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &types.InvitedUser{u.ID, u.OrgID, u.Email, u.Name, u.SlackID, u.Groups}, nil
	// ednQuery := fmt.Sprintf(`{:query {
	// 	:find [(pull ?u [*])]
	// 	:in [orgid email]
	// 	:where [[?u :invited-user/org orgid]
	// 			[?u :invited-user/email email]]}
	// 	:in-args [%q %q]}`, ctx.OrgID, email)

	// data, err := ctx.Query(ednQuery)
	// if err != nil {
	// 	return nil, err
	// }

	// var invitedUser [][]types.InvitedUser
	// if err := edn.Unmarshal(data, &invitedUser); err != nil {
	// 	return nil, err
	// }

	// if len(invitedUser) == 0 {
	// 	return nil, nil
	// }

	// return &invitedUser[0][0], nil
}

func UpdateInvitedUser(ctx *storagev2.Context, u *types.InvitedUser) error {
	return pgrest.New("/users_update").Create(map[string]any{
		"id":       u.ID,
		"subject":  u.ID,
		"org_id":   ctx.OrgID,
		"name":     u.Name,
		"email":    u.Email,
		"verified": false,
		"status":   "reviewing",
		"slack_id": u.SlackID,
	}).Error()
	// _, err := ctx.Put(user)
	// return err
}
