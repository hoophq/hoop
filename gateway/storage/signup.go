package storage

import (
	"github.com/runopsio/hoop/domain"
	"olympos.io/encoding/edn"
)

func (s *Storage) Signup(org *domain.Org, user *domain.User) (txId int64, err error) {
	orgPayload := entityToMap(org)
	userPayload := entityToMap(user)

	entities := []map[string]interface{}{orgPayload, userPayload}
	txId, err = s.persistEntities(entities)
	if err != nil {
		return 0, err
	}

	return txId, nil
}

func (s *Storage) GetLoggedUser(email string) (*domain.Context, error) {
	user, err := s.getUser(email)
	if err != nil {
		return nil, err
	}

	org, err := s.getOrg(user.Org)
	if err != nil {
		return nil, err
	}

	return &domain.Context{
		Org:  org,
		User: user,
	}, nil
}

func (s *Storage) getUser(email string) (*domain.User, error) {
	var payload = `{:query {
		:find [(pull ?user [*])] 
		:where [[?user :user/email "` +
		email + `"]]}}`

	b, err := s.query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var user []domain.User
	if err := edn.Unmarshal(b, &user); err != nil {
		return nil, err
	}

	return &user[0], nil
}

func (s *Storage) getOrg(orgId string) (*domain.Org, error) {
	var payload = `{:query {
		:find [(pull ?org [*])] 
		:where [[?org :xt/id "` +
		orgId + `"]]}}`

	b, err := s.query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var org []domain.Org
	if err := edn.Unmarshal(b, &org); err != nil {
		return nil, err
	}

	return &org[0], nil
}
