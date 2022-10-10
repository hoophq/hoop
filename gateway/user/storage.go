package user

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) ContextByEmail(email string) (*Context, error) {
	u, err := s.getUser(email)
	if err != nil {
		return nil, err
	}

	if u == nil {
		return nil, nil
	}

	o, err := s.getOrg(u.Org)
	if err != nil {
		return nil, err
	}

	return &Context{
		Org:  o,
		User: u,
	}, nil
}

func (s *Storage) Signup(org *Org, user *User) (txId int64, err error) {
	orgPayload := st.EntityToMap(org)
	userPayload := st.EntityToMap(user)

	entities := []map[string]interface{}{orgPayload, userPayload}
	txId, err = s.PersistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) getUser(email string) (*User, error) {
	var payload = `{:query {
		:find [(pull ?user [*])] 
		:where [[?user :user/email "` +
		email + `"]]}}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var u []User
	if err := edn.Unmarshal(b, &u); err != nil {
		return nil, err
	}

	if len(u) == 0 {
		return nil, nil
	}

	return &u[0], nil
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

	return &org[0], nil
}

func (s *Storage) FindLogin(state string) (*login, error) {
	b, err := s.GetEntity(state)
	if err != nil {
		return nil, err
	}

	var login login
	if err := edn.Unmarshal(b, &login); err != nil {
		return nil, err
	}

	return &login, nil
}

func (s *Storage) PersistLogin(login *login) (int64, error) {
	payload := st.EntityToMap(login)

	txId, err := s.PersistEntities([]map[string]interface{}{payload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
