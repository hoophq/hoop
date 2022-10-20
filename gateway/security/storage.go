package security

import (
	st "github.com/runopsio/hoop/gateway/storage"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

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

	txId, err := s.PersistEntities([]map[string]any{payload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
