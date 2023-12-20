package security

import (
	pglogin "github.com/runopsio/hoop/gateway/pgrest/login"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type Storage struct{}

func (s *Storage) FindLogin(state string) (*login, error) {
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

func (s *Storage) PersistLogin(login *login) (int64, error) {
	return 0, pglogin.New().Upsert(&types.Login{
		ID:       login.Id,
		Redirect: login.Redirect,
		Outcome:  string(login.Outcome),
		SlackID:  login.SlackID,
	})
}
