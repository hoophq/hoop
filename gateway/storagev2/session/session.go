package sessionstorage

import (
	"fmt"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

func Put(storage *storagev2.Context, session types.Session) error {
	if pgrest.Rollout {
		return pgsession.New().Upsert(storage, session)
	}
	_, err := storage.Put(session)
	return err
}

func FindOne(storageCtx *storagev2.Context, sessionID string) (*types.Session, error) {
	if pgrest.Rollout {
		return pgsession.New().FetchOne(storageCtx, sessionID)
	}
	payload := fmt.Sprintf(`{:query {
		:find [(pull ?session [*])] 
		:in [org-id session-id user-id]
		:where [[?session :session/org-id org-id]
          	[?session :xt/id session-id]
						[?session :session/user-id user-id]]}
		:in-args [%q %q %q]}`, storageCtx.OrgID, sessionID, storageCtx.UserID)

	b, err := storageCtx.Query(payload)
	if err != nil {
		return nil, err
	}

	var sessions [][]types.Session
	if err := edn.Unmarshal(b, &sessions); err != nil {
		return nil, err
	}

	if len(sessions) > 0 {
		return &sessions[0][0], nil
	}

	return nil, nil
}
