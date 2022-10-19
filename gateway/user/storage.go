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

func (s *Storage) Persist(user interface{}) (int64, error) {
	payload := st.EntityToMap(user)

	txId, err := s.PersistEntities([]map[string]interface{}{payload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) GetByEmail(email string) (*User, error) {
	var payload = `{:query {
		:find [(pull ?user [*])] 
		:in [email]
		:where [[?user :user/email email]]}
		:in-args ["` + email + `"]}`

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

	if len(org) == 0 {
		return nil, nil
	}

	return &org[0], nil
}
