package pglogin

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type login struct{}

func New() *login { return &login{} }

func (l *login) FetchOne(subject string) (*types.Login, error) {
	return nil, fmt.Errorf("long.FetchOne: not implemented")
}

func (l *login) Upsert(lg *types.Login) error {
	var slackID *string
	if lg.SlackID != "" {
		slackID = &lg.SlackID
	}
	return pgrest.New("/login").Upsert(map[string]any{
		"id":       lg.ID,
		"redirect": lg.Redirect,
		"outcome":  lg.Outcome,
		"slack_id": slackID,
	}).Error()
}
