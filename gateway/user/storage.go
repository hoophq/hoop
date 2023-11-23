package user

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	st "github.com/runopsio/hoop/gateway/storage"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindById(identifier string) (*Context, error) {
	path := fmt.Sprintf("/users?select=*,org(id,name)&subject=eq.%v", identifier)
	if _, err := uuid.Parse(identifier); err == nil {
		path = fmt.Sprintf("/users?select=*,org(id,name)&or=(subject.eq.%v,id.eq.%v)", identifier, identifier)
	}
	var u pgrest.User
	if err := pgrest.New(path).FetchOne().DecodeInto(&u); err != nil {
		if err == pgrest.ErrNotFound {
			return &Context{}, nil
		}
		return nil, err
	}
	return &Context{
			User: &User{u.ID, u.Org.ID, u.Name, u.Email, StatusType(u.Status), u.SlackID, u.Groups},
			Org:  &Org{Id: u.Org.ID, Name: u.Org.Name}},
		nil

	// b, err := s.GetEntity(identifier)
	// if err != nil {
	// 	return nil, err
	// }

	// if b == nil {
	// 	return c, nil
	// }

	// var u User
	// if err := edn.Unmarshal(b, &u); err != nil {
	// 	return nil, err
	// }

	// o, err := s.getOrg(u.Org)
	// if err != nil {
	// 	return nil, err
	// }

	// c.User = &u
	// c.Org = o

	// return c, nil
}

// func (s *Storage) FindByEmail(ctx *Context, email string) (*User, error) {
// 	qs := fmt.Sprintf(`{:query {
// 		:find [(pull ?u [*])]
// 		:in [orgid email]
// 		:where [[?u :user/org orgid]
// 				[?u :user/email email]]}
// 		:in-args [%q %q]}`, ctx.Org.Id, email)
// 	data, err := s.Query([]byte(qs))
// 	if err != nil {
// 		return nil, err
// 	}
// 	var user []User
// 	if err := edn.Unmarshal(data, &user); err != nil {
// 		return nil, err
// 	}

// 	if len(user) > 1 {
// 		return nil, fmt.Errorf("user storage is inconsistent")
// 	}

// 	if len(user) == 0 {
// 		return nil, nil
// 	}

// 	return &user[0], nil
// }

func (s *Storage) FindBySlackID(ctx *Org, slackID string) (*User, error) {
	qs := fmt.Sprintf(`{:query {
		:find [(pull ?u [*])] 
		:in [orgid slack-id]
		:where [[?u :user/org orgid]
				[?u :user/slack-id slack-id]
				[?u :user/status "active"]]}
		:in-args [%q %q]}`, ctx.Id, slackID)
	data, err := s.Query([]byte(qs))
	if err != nil {
		return nil, err
	}
	var user []User
	if err := edn.Unmarshal(data, &user); err != nil {
		return nil, err
	}

	if len(user) > 1 {
		return nil, fmt.Errorf("user storage is inconsistent")
	}

	if len(user) == 0 {
		return nil, nil
	}

	return &user[0], nil
}

// TODO: SECURITY BUG - need to lookup based on the context organization!
func (s *Storage) FindInvitedUser(email string) (*InvitedUser, error) {
	client := pgrest.New("/users?select=*,org(id,name)&email=eq.%v&verified=is.false", email)
	var u pgrest.User
	if err := client.FetchOne().DecodeInto(&u); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &InvitedUser{u.ID, u.OrgID, u.Email, u.Name, u.SlackID, u.Groups}, nil

	// var payload = `{:query {
	// 	:find [(pull ?invited-user [*])]
	// 	:in [email]
	// 	:where [[?invited-user :invited-user/email email]]}
	// 	:in-args ["` + email + `"]}`

	// b, err := s.Query([]byte(payload))
	// if err != nil {
	// 	return nil, err
	// }

	// var invitedUser []InvitedUser
	// if err := edn.Unmarshal(b, &invitedUser); err != nil {
	// 	return nil, err
	// }

	// if len(invitedUser) == 0 {
	// 	return nil, nil
	// }

	// return &invitedUser[0], nil
}

func (s *Storage) FindAll(ctx *Context) ([]User, error) {
	client := pgrest.New("/users?select=*,org(id,name)&org_id=eq.%v&verified=is.true", ctx.Org.Id)
	var users []pgrest.User
	if err := client.List().DecodeInto(&users); err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	var xtdbUsers []User
	for _, u := range users {
		xtdbUsers = append(xtdbUsers,
			User{u.ID, u.OrgID, u.Name, u.Email, StatusType(u.Status), u.SlackID, u.Groups})
	}
	return xtdbUsers, nil

	// var payload = `{:query {
	// 	:find [(pull ?user [*])]
	// 	:in [org]
	// 	:where [[?user :user/org org]]}
	// 	:in-args ["` + context.Org.Id + `"]}`

	// b, err := s.Query([]byte(payload))
	// if err != nil {
	// 	return nil, err
	// }

	// var users []User
	// if err := edn.Unmarshal(b, &users); err != nil {
	// 	return nil, err
	// }

	// return users, nil
}

func (s *Storage) Persist(user any) (res int64, err error) {
	switch v := user.(type) {
	case *User:
		var existentUsr pgrest.User
		if err := pgrest.New("/users?select=*,org(id,name)&org_id=eq.%v&verified=is.true", v.Org).
			FetchOne().
			DecodeInto(&existentUsr); err != nil && err != pgrest.ErrNotFound {
			return 0, fmt.Errorf("failed fetching user: %v", err)
		}
		userID := uuid.NewString()
		if existentUsr.ID != "" {
			userID = existentUsr.ID
		}

		defer func() {
			if err == nil {
				err = pgrest.New("/rpc/update_groups").RpcCreate(map[string]any{
					"user_id": userID,
					"org_id":  v.Org,
					"groups":  v.Groups,
				}).Error()
			}
		}()
		if existentUsr.ID != "" {
			return 0, pgrest.New("/users_update?id=eq.%v", userID).Patch(map[string]any{
				"name":     v.Name,
				"verified": true,
				"status":   v.Status,
				"slack_id": v.SlackID,
			}).Error()
		}
		return 0, pgrest.New("/users_update").Create(map[string]any{
			"id":       userID,
			"subject":  v.Id,
			"org_id":   v.Org,
			"name":     v.Name,
			"email":    v.Email,
			"verified": true,
			"status":   v.Status,
			"slack_id": v.SlackID,
		}).Error()
	case *Org:
		defer func() {
			if err == nil {
				payload := st.EntityToMap(user)
				_, _ = s.PersistEntities([]map[string]any{payload})
			}
		}()
		payload := map[string]any{"id": v.Id, "name": v.Name}
		return 0, pgrest.New("/orgs").Create(payload).Error()
	default:
		return 0, fmt.Errorf("failed type casting to user or org, found=%T", user)
	}
	// payload := st.EntityToMap(user)

	// txId, err := s.PersistEntities([]map[string]any{payload})
	// if err != nil {
	// 	return 0, err
	// }

	// return txId, nil
}

func (s *Storage) GetOrgNameByID(orgID string) (*Org, error) {
	var u pgrest.Org
	if err := pgrest.New("/orgs?id=eq.%s", orgID).
		FetchOne().
		DecodeInto(&u); err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &Org{u.ID, u.Name, false}, nil

	// ednQuery := fmt.Sprintf(`{:query {
	// 	:find [(pull ?o [*])]
	// 	:in [orgid]
	// 	:where [[?o :xt/id orgid]]}
	//     :in-args [%q]}`, orgID)
	// b, err := s.Query([]byte(ednQuery))
	// if err != nil {
	// 	return nil, err
	// }

	// var u []Org
	// if err := edn.Unmarshal(b, &u); err != nil {
	// 	return nil, err
	// }

	// if len(u) == 0 {
	// 	return nil, nil
	// }
	// return &u[0], nil
}

func (s *Storage) GetOrgByName(name string) (*Org, error) {
	client := pgrest.New(fmt.Sprintf("/orgs?name=eq.%v", name))
	var org pgrest.Org
	err := client.FetchOne().DecodeInto(&org)
	if err == pgrest.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Org{Id: org.ID, Name: org.Name}, nil

	// var payload = `{:query {
	// 	:find [(pull ?org [*])]
	// 	:in [name]
	// 	:where [[?org :org/name name]]}
	// 	:in-args ["` + name + `"]}`

	// b, err := s.Query([]byte(payload))
	// if err != nil {
	// 	return nil, err
	// }

	// var u []Org
	// if err := edn.Unmarshal(b, &u); err != nil {
	// 	return nil, err
	// }

	// if len(u) == 0 {
	// 	return nil, nil
	// }

	// return &u[0], nil
}

// used when sending reports
func (s *Storage) FindByGroups(context *Context, groups []string) ([]User, error) {
	if len(groups) == 0 {
		return make([]User, 0), nil
	}

	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?user [*])]
	  	:in [org [g ...]]
    	:where [[?user :user/org org]
				[?user :user/groups g]]}
		:in-args ["%s" %q]}}`, context.Org.Id, groups)

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var users []User
	if err := edn.Unmarshal(b, &users); err != nil {
		return nil, err
	}

	return users, nil
}

func (s *Storage) ListAllGroups(context *Context) ([]string, error) {
	if pgrest.WithPostgres(context) {
		return pgusers.New().ListGroups(context)
	}
	var payload = fmt.Sprintf(`{:query {
		:find [(distinct g)]
	  	:in [org]
    	:where [[?user :user/org org]
				[?user :user/groups g]]}
		:in-args ["%s"]}}`, context.Org.Id)

	b, err := s.QueryRawAsJson([]byte(payload))
	if err != nil {
		return nil, err
	}

	var result [][][]string
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}

	groups := make([]string, 0)
	if len(result) > 0 {
		groups = result[0][0]
	}

	return groups, nil
}

// func (s *Storage) getOrg(orgId string) (*Org, error) {
// 	var payload = `{:query {
// 		:find [(pull ?org [*])]
// 		:where [[?org :xt/id "` +
// 		orgId + `"]]}}`

// 	b, err := s.Query([]byte(payload))
// 	if err != nil {
// 		return nil, err
// 	}

// 	var org []Org
// 	if err := edn.Unmarshal(b, &org); err != nil {
// 		return nil, err
// 	}

// 	if len(org) == 0 {
// 		return nil, nil
// 	}

// 	return &org[0], nil
// }

// used when sending reports and by slack plugin
func (s *Storage) FindOrgs() ([]Org, error) {
	var payload = `{:query 
                    {:find [(pull p [*])]
					 :where [[p :org/name s]
							 [p :xt/id i]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var orgs []Org
	if err := edn.Unmarshal(b, &orgs); err != nil {
		return nil, err
	}

	return orgs, nil
}
