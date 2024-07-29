package sessionstorage

import (
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	"github.com/hoophq/hoop/gateway/storagev2"
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

func List(ctx *storagev2.Context, opts ...*openapi.SessionOption) (*openapi.SessionList, error) {
	var options []*pgrest.SessionOption
	for _, opt := range opts {
		options = append(options, &pgrest.SessionOption{
			OptionKey: pgrest.SessionOptionKey(opt.OptionKey),
			OptionVal: opt.OptionVal,
		})
	}
	sl, err := pgsession.New().FetchAll(ctx, options...)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	sessionList := &openapi.SessionList{
		Total:       sl.Total,
		HasNextPage: sl.HasNextPage,
	}
	for _, s := range sl.Items {
		// _, eventSize := s.GetBlobStream()
		sessionList.Items = append(sessionList.Items, openapi.Session{
			ID:          s.ID,
			OrgID:       s.OrgID,
			Script:      openapi.SessionScriptType{"data": ""}, // do not show the script on listing
			Labels:      s.Labels,
			Metadata:    s.Metadata,
			UserEmail:   s.UserEmail,
			UserID:      s.UserID,
			UserName:    s.UserName,
			Type:        s.ConnectionType,
			Connection:  s.Connection,
			Verb:        s.Verb,
			Status:      s.Status,
			EventStream: nil,
			// NonIndexedStream: nil,
			StartSession: s.GetCreatedAt(),
			EndSession:   s.GetEndedAt(),
		})
	}
	return sessionList, nil
}
