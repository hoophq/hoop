package sessionstorage

import (
	"github.com/hoophq/hoop/gateway/pgrest"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// FindOne doe not enforce fetching the session by its user.
// However, this is somehow protected by obscurity,
// since the user won't know the session id of a distinct user.
func FindOne(ctx pgrest.OrgContext, sessionID string) (*types.Session, error) {
	sess, err := pgsession.New().FetchOne(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, nil
	}
	if sess.NonIndexedStream != nil {
		nonIndexedStreams := sess.NonIndexedStream["stream"]
		for _, i := range nonIndexedStreams {
			sess.EventStream = append(sess.EventStream, i)
		}
	}
	return sess, nil
}
