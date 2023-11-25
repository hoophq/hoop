package security

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	st "github.com/runopsio/hoop/gateway/storage"
)

type (
	Storage struct {
		*st.Storage
	}
)

func (s *Storage) FindLogin(state string) (*login, error) {
	client := pgrest.New(fmt.Sprintf("/login?id=eq.%v", state))
	var l pgrest.Login
	err := client.FetchOne().DecodeInto(&l)
	switch err {
	case pgrest.ErrNotFound:
		return nil, nil
	case nil:
		return &login{l.ID, l.Redirect, outcomeType(l.Outcome), l.SlackID}, nil
	default:
		return nil, err
	}

	// b, err := s.GetEntity(state)
	// if err != nil {
	// 	return nil, err
	// }

	// if b == nil {
	// 	return nil, nil
	// }

	// var login login
	// if err := edn.Unmarshal(b, &login); err != nil {
	// 	return nil, err
	// }

	// return &login, nil
}

func (s *Storage) PersistLogin(login *login) (int64, error) {
	// TODO: should perform an upsert instead of post only
	client := pgrest.New("/login")
	req := map[string]string{"id": login.Id, "redirect": login.Redirect}
	if login.Outcome != "" {
		req["outcome"] = string(login.Outcome)
	}
	if login.SlackID != "" {
		req["slack_id"] = login.SlackID
	}
	return 0, client.Upsert(req).Error()

	// payload := st.EntityToMap(login)

	// txId, err := s.PersistEntities([]map[string]any{payload})
	// if err != nil {
	// 	return 0, err
	// }

	// return txId, nil
}
