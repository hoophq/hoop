package user

import (
	"encoding/json"
	"fmt"

	st "github.com/runopsio/hoop/gateway/storage"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindById(identifier string) (*Context, error) {
	c := &Context{}

	b, err := s.GetEntity(identifier)
	if err != nil {
		return nil, err
	}

	if b == nil {
		return c, nil
	}

	var u User
	if err := edn.Unmarshal(b, &u); err != nil {
		return nil, err
	}

	o, err := s.getOrg(u.Org)
	if err != nil {
		return nil, err
	}

	c.User = &u
	c.Org = o

	return c, nil
}

func (s *Storage) FindByEmail(ctx *Context, email string) (*User, error) {
	qs := fmt.Sprintf(`{:query {
		:find [(pull ?u [*])] 
		:in [orgid email]
		:where [[?u :user/org orgid]
				[?u :user/email email]]}
		:in-args [%q %q]}`, ctx.Org.Id, email)
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

func (s *Storage) FindInvitedUser(email string) (*InvitedUser, error) {
	var payload = `{:query {
		:find [(pull ?invited-user [*])] 
		:in [email]
		:where [[?invited-user :invited-user/email email]]}
		:in-args ["` + email + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var invitedUser []InvitedUser
	if err := edn.Unmarshal(b, &invitedUser); err != nil {
		return nil, err
	}

	if len(invitedUser) == 0 {
		return nil, nil
	}

	return &invitedUser[0], nil
}

func (s *Storage) FindAll(context *Context) ([]User, error) {
	var payload = `{:query {
		:find [(pull ?user [*])] 
		:in [org]
		:where [[?user :user/org org]]}
		:in-args ["` + context.Org.Id + `"]}`

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

func (s *Storage) Persist(user any) (int64, error) {
	payload := st.EntityToMap(user)

	txId, err := s.PersistEntities([]map[string]any{payload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) Signup(org *Org, user *User) (txId int64, err error) {
	orgPayload := st.EntityToMap(org)
	userPayload := st.EntityToMap(user)

	entities := []map[string]any{orgPayload, userPayload}
	txId, err = s.PersistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) GetOrgNameByID(orgID string) (*Org, error) {
	ednQuery := fmt.Sprintf(`{:query {
		:find [(pull ?o [*])]
		:in [orgid]
		:where [[?o :xt/id orgid]]}
        :in-args [%q]}`, orgID)
	b, err := s.Query([]byte(ednQuery))
	if err != nil {
		return nil, err
	}

	var u []Org
	if err := edn.Unmarshal(b, &u); err != nil {
		return nil, err
	}

	if len(u) == 0 {
		return nil, nil
	}
	return &u[0], nil
}

func (s *Storage) GetOrgByName(name string) (*Org, error) {
	var payload = `{:query {
		:find [(pull ?org [*])] 
		:in [name]
		:where [[?org :org/name name]]}
		:in-args ["` + name + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var u []Org
	if err := edn.Unmarshal(b, &u); err != nil {
		return nil, err
	}

	if len(u) == 0 {
		return nil, nil
	}

	return &u[0], nil
}

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

func (s *Storage) getOrg(orgId string) (*Org, error) {
	var payload = `{:query {
		:find [(pull ?org [*])] 
		:where [[?org :xt/id "` +
		orgId + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var org []Org
	if err := edn.Unmarshal(b, &org); err != nil {
		return nil, err
	}

	if len(org) == 0 {
		return nil, nil
	}

	return &org[0], nil
}

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
