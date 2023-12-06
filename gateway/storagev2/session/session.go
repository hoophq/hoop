package sessionstorage

import (
	"fmt"
	"strings"
	"time"

	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	pgsession "github.com/runopsio/hoop/gateway/pgrest/session"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

const (
	defaultLimit  int = 100
	defaultOffset int = 0
)

func Put(storage *storagev2.Context, session types.Session) error {
	if pgrest.Rollout {
		return pgsession.New().Upsert(storage, session)
	}
	_, err := storage.Put(session)
	return err
}

// FindOne doe not enforce fetching the session by its user.
// However, this is somehow protected by obscurity,
// since the user won't know the session id of a distinct user.
func FindOne(storageCtx *storagev2.Context, sessionID string) (*types.Session, error) {
	if pgrest.Rollout {
		sess, err := pgsession.New().FetchOne(storageCtx, sessionID)
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
	// the user id is used to enforce querying by the user when using xtdb
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

	if len(sessions) == 0 {
		return nil, nil
	}
	s := &sessions[0][0]
	if s.NonIndexedStream != nil {
		nonIndexedStreams := s.NonIndexedStream["stream"]
		for _, i := range nonIndexedStreams {
			s.EventStream = append(s.EventStream, i)
		}
	}
	return s, nil
}

func FindReviewBySID(ctx *storagev2.Context, sessionID string) (*types.Review, error) {
	if pgrest.Rollout {
		return pgreview.New().FetchOneBySid(ctx, sessionID)
	}
	var payload = fmt.Sprintf(`{:query {
		:find [(pull ?r [*])]
		:in [session-id]
		:where [[?r :review/session session-id]
				[?r :review/connection connid]
				[?c :xt/id connid]]}
        :in-args [%q]}`, sessionID)

	b, err := ctx.Query(payload)
	if err != nil {
		return nil, err
	}

	var reviews [][]types.Review
	if err := edn.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}

	if len(reviews) == 0 {
		return nil, nil
	}

	return &reviews[0][0], nil
}

func List(ctx *storagev2.Context, opts ...*types.SessionOption) (*types.SessionList, error) {
	if pgrest.Rollout {
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
		sessionList := &types.SessionList{
			Total:       sl.Total,
			HasNextPage: sl.HasNextPage,
		}
		for _, s := range sl.Items {
			_, eventSize := s.GetBlobStream()
			sessionList.Items = append(sessionList.Items, types.Session{
				ID:               s.ID,
				OrgID:            s.OrgID,
				Script:           types.SessionScript{"data": ""}, // do not show the script on listing
				Labels:           s.Labels,
				UserEmail:        s.UserEmail,
				UserID:           s.UserID,
				UserName:         s.UserName,
				Type:             s.ConnectionType,
				Connection:       s.Connection,
				Verb:             s.Verb,
				Status:           s.Status,
				DlpCount:         s.GetRedactCount(),
				EventStream:      nil,
				NonIndexedStream: nil,
				EventSize:        eventSize,
				StartSession:     s.GetCreatedAt(),
				EndSession:       s.GetEndedAt(),
			})
		}
		return sessionList, nil
	}
	inArgsEdn, limit, offset := xtdbQueryParams(ctx.OrgID, opts...)
	var queryCountResult []any
	if err := queryDecoder(ctx, `{:query {
		:find [id]
		:keys [xt/id]
		:in [org-id arg-user arg-type arg-conn arg-start-date arg-end-date]
		:where [[a :session/org-id org-id]
				[a :xt/id id]
				[a :session/user-id usr-id]
				[a :session/type typ]
				[a :session/connection conn]
				[a :session/start-date start-date]
				(or [(= arg-user nil)]
					[(= usr-id arg-user)])
				(or [(= arg-type nil)]
					[(= typ arg-type)])
				(or [(= arg-conn nil)]
					[(= conn arg-conn)])
				(or [(= arg-start-date nil)]
					[(> start-date arg-start-date)])
				(or [(= arg-end-date nil)]
					[(< start-date arg-end-date)])]}
	 :in-args [%s]}`, &queryCountResult,
		inArgsEdn); err != nil {
		return nil, fmt.Errorf("failed performing counting session items, err=%v", err)
	}
	sessionList := &types.SessionList{
		Total:       int64(len(queryCountResult)),
		HasNextPage: false,
	}
	err := queryDecoder(ctx, `
		{:query {
			:find [id usr usr-id usr-name status script labels typ conn verb event-size start-date end-date dlp-count]
			:keys [xt/id session/user session/user-id session/user-name session/status session/script
				   session/labels session/type session/connection session/verb session/event-size
				   session/start-date session/end-date session/dlp-count]
			:in [org-id arg-user arg-type arg-conn arg-start-date arg-end-date]
			:where [[a :session/org-id org-id]
					[a :xt/id id]
					[a :session/user usr]
					[a :session/user-id usr-id]
					[a :session/user-name usr-name]
					[(get-attr a :session/status "") [status]]
					[(get-attr a :session/script nil) [script]]
					[(get-attr a :session/labels nil) [labels]]
					[a :session/type typ]
					[a :session/connection conn]
					[a :session/verb verb]
					[a :session/event-size event-size]
					[a :session/start-date start-date]
					[a :session/end-date end-date]
					[(get-attr a :session/dlp-count 0) [dlp-count]]
					(or [(= arg-user nil)]
						[(= usr-id arg-user)])
					(or [(= arg-type nil)]
						[(= typ arg-type)])
					(or [(= arg-conn nil)]
						[(= conn arg-conn)])
					(or [(= arg-start-date nil)]
						[(> start-date arg-start-date)])
					(or [(= arg-end-date nil)]
						[(< start-date arg-end-date)])]
			:order-by [[start-date :desc]]
			:limit %v
			:offset %v}
		:in-args [%s]}`,
		&sessionList.Items,
		limit, offset, inArgsEdn)
	sessionList.HasNextPage = len(sessionList.Items) == limit
	return sessionList, err
}

func xtdbQueryParams(orgID string, opts ...*types.SessionOption) (string, int, int) {
	inArgsEdn := fmt.Sprintf(`%q`, orgID)
	limit := defaultLimit
	offset := defaultOffset
	for _, keyOption := range types.AvailableSessionOptions {
		optionFound := false
		for _, opt := range opts {
			if opt.OptionKey == types.SessionOptionLimit {
				val, _ := opt.OptionVal.(int)
				limit = val
				continue
			} else if opt.OptionKey == types.SessionOptionOffset {
				val, _ := opt.OptionVal.(int)
				offset = val
				continue
			}
			if keyOption == opt.OptionKey {
				optionFound = true
				val, _ := edn.Marshal(opt.OptionVal)
				inArgsEdn = fmt.Sprintf("%v %v", inArgsEdn, string(val))
				break
			}
		}
		if !optionFound && keyOption != types.SessionOptionLimit && keyOption != types.SessionOptionOffset {
			nilVal, _ := edn.Marshal(nil)
			inArgsEdn = fmt.Sprintf("%v %v", inArgsEdn, string(nilVal))
		}
	}
	return inArgsEdn, limit, offset
}

func queryDecoder(ctx *storagev2.Context, query string, into any, args ...any) error {
	qs := fmt.Sprintf(query, args...)
	httpBody, err := ctx.Query(qs)
	if err != nil {
		return err
	}
	if strings.Contains(string(httpBody), ":xtdb.error") {
		return fmt.Errorf(string(httpBody))
	}
	return edn.Unmarshal(httpBody, into)
}

func ListAllSessionsID(fromDate time.Time) ([]*types.Session, error) {
	if pgrest.Rollout {
		return pgsession.New().FetchAllFromDate(fromDate)
	}
	ctx := storagev2.NewStorage(nil)
	query := fmt.Sprintf(`
    {:query {
        :find [id org-id]
        :in [arg-start-date]
        :keys [xt/id session/org-id]
        :where [[?s :xt/id id]
                [?s :session/org-id org-id]
                [?s :session/start-date start-date]
                [(> start-date arg-start-date)]]}
    :in-args [#inst%q]}`, fromDate.Format(time.RFC3339))
	httpBody, err := ctx.Query(query)
	if err != nil {
		return nil, err
	}
	var sessionList []*types.Session
	if strings.Contains(string(httpBody), ":xtdb.error") {
		return nil, fmt.Errorf(string(httpBody))
	}
	return sessionList, edn.Unmarshal(httpBody, &sessionList)
}
