package security

import (
	"github.com/runopsio/hoop/gateway/pgrest"
	pglogin "github.com/runopsio/hoop/gateway/pgrest/login"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindLogin(state string) (*login, error) {
	if pgrest.Rollout {
		l, err := pglogin.New().FetchOne(state)
		if err != nil {
			return nil, err
		}
		if l == nil {
			return nil, nil
		}
		return &login{
			Id:       l.ID,
			Redirect: l.Redirect,
			Outcome:  outcomeType(l.Outcome),
			SlackID:  l.SlackID}, nil
	}
	b, err := s.GetEntity(state)
	if err != nil {
		return nil, err
	}

	if b == nil {
		return nil, nil
	}

	var login login
	if err := edn.Unmarshal(b, &login); err != nil {
		return nil, err
	}

	return &login, nil
}

func (s *Storage) PersistLogin(login *login) (int64, error) {
	if pgrest.Rollout {
		return 0, pglogin.New().Upsert(&types.Login{
			ID:       login.Id,
			Redirect: login.Redirect,
			Outcome:  string(login.Outcome),
			SlackID:  login.SlackID,
		})
	}
	payload := st.EntityToMap(login)

	txId, err := s.PersistEntities([]map[string]any{payload})
	if err != nil {
		return 0, err
	}

	return txId, nil
}
