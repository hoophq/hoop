package pglogin

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type login struct{}

func New() *login { return &login{} }

func (l *login) FetchOne(state string) (*pgrest.Login, error) {
	var login pgrest.Login
	err := pgrest.New(fmt.Sprintf(`/login?id=eq.%v&outcome=eq.`, state)).
		FetchOne().
		DecodeInto(&login)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &login, nil
}

func (l *login) Upsert(lg *types.Login) error {
	var slackID *string
	if lg.SlackID != "" {
		slackID = &lg.SlackID
	}
	// store accepts max of 200 chars for this attribute
	if len(lg.Outcome) >= 200 {
		lg.Outcome = lg.Outcome[:195] + " ..."
	}
	return pgrest.New("/login").Upsert(map[string]any{
		"id":         lg.ID,
		"redirect":   lg.Redirect,
		"outcome":    lg.Outcome,
		"slack_id":   slackID,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}).Error()
}
