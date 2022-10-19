package client

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindAll(context *user.Context) ([]Client, error) {
	var payload = `{:query {
		:find [(pull ?client [*])] 
		:id [id]
		:where [[?client :client/user id]]}
		:in-args ["` + context.User.Id + `"]}`

	b, err := s.Query([]byte(payload))
	if err != nil {
		return nil, err
	}

	var clients []Client
	if err := edn.Unmarshal(b, &clients); err != nil {
		return nil, err
	}

	return clients, nil
}

func (s *Storage) Persist(client *Client) (int64, error) {
	clientPayload := st.EntityToMap(client)

	txId, err := s.PersistEntities([]map[string]interface{}{clientPayload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
