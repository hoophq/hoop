package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/log"
	st "github.com/runopsio/hoop/gateway/storage"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Storage struct {
		*st.Storage
	}
	GenericStorageWriter struct {
		persistFn func(*user.Context, *Session) (*st.TxResponse, error)
	}
)

const (
	defaultLimit  int = 100
	defaultOffset int = 0
)

func WithOption(optKey SessionOptionKey, val any) *SessionOption {
	return &SessionOption{optionKey: optKey, optionVal: val}
}

func xtdbQueryParams(orgID string, opts ...*SessionOption) (string, int, int) {
	inArgsEdn := fmt.Sprintf(`%q`, orgID)
	limit := defaultLimit
	offset := defaultOffset
	for _, keyOption := range availableSessionOptions {
		optionFound := false
		for _, opt := range opts {
			if opt.optionKey == OptionLimit {
				val, _ := opt.optionVal.(int)
				limit = val
				continue
			} else if opt.optionKey == OptionOffset {
				val, _ := opt.optionVal.(int)
				offset = val
				continue
			}
			if keyOption == opt.optionKey {
				optionFound = true
				val, _ := edn.Marshal(opt.optionVal)
				inArgsEdn = fmt.Sprintf("%v %v", inArgsEdn, string(val))
				break
			}
		}
		if !optionFound && keyOption != OptionLimit && keyOption != OptionOffset {
			nilVal, _ := edn.Marshal(nil)
			inArgsEdn = fmt.Sprintf("%v %v", inArgsEdn, string(nilVal))
		}
	}
	return inArgsEdn, limit, offset
}

func (s *Storage) Persist(context *user.Context, session *Session) (*st.TxResponse, error) {
	session.OrgID = context.Org.Id
	if session.OrgID == "" || session.ID == "" {
		return nil, fmt.Errorf("session id and organization must not be empty")
	}
	if session.EventStream != nil {
		return nil, fmt.Errorf("accept only non indexed event stream")
	}

	return s.SubmitPutTx(session)
}

func (s *Storage) PersistStatus(status *SessionStatus) (*st.TxResponse, error) {
	if status.ID == "" || status.SessionID == "" {
		return nil, fmt.Errorf("session id and xt/id must not be empty")
	}
	return s.SubmitPutTx(status)
}

func (s *Storage) EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error) {
	var obj [][]SessionStatus
	argUserID := fmt.Sprintf(`"%s"`, ctx.User.Id)
	if ctx.User.IsAdmin() {
		argUserIDBytes, _ := edn.Marshal(nil)
		argUserID = string(argUserIDBytes)
	}
	err := s.queryDecoder(`{:query {
		:find [(pull ?o [*])]
        :in [org-id session-id arg-user-id]
		:where [[?s :xt/id session-id]
                [?s :session/org-id org-id]
				[?s :session/user-id user-id]
				(or [(= arg-user-id nil)]
				[(= arg-user-id user-id)])
                [?o :session-status/session-id ?s]]}
        :in-args [%q %q %v]}`, &obj, ctx.Org.Id, sessionID, argUserID)
	if err != nil {
		return nil, fmt.Errorf("failed fetching previous session status, err=%v", err)
	}
	if len(obj) > 0 {
		statusID := obj[0][0].ID
		entityHistory, err := s.Storage.GetEntityHistory(statusID, "asc", true)
		if err != nil {
			return nil, err
		}
		var historyList []SessionStatusHistory
		return historyList, edn.Unmarshal(entityHistory, &historyList)
	}
	return nil, nil
}

func (s *Storage) ValidateSessionID(sessionID string) error {
	var res [][]string
	err := s.queryDecoder(`{:query {
		:find [session-id]
        :in [session-id]
		:where [[?s :xt/id session-id]]}
        :in-args [%q]}`, &res, sessionID)
	if err != nil {
		return fmt.Errorf("failed validating session id, err=%v", err)
	}
	if len(res) > 0 {
		return fmt.Errorf("session id %v already exists", sessionID)
	}
	return nil
}

func (s *Storage) FindAll(ctx *user.Context, opts ...*SessionOption) (*SessionList, error) {
	inArgsEdn, limit, offset := xtdbQueryParams(ctx.Org.Id, opts...)
	var queryCountResult []any
	if err := s.queryDecoder(`{:query {
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
	sessionList := &SessionList{
		Total:       len(queryCountResult),
		HasNextPage: false,
	}
	err := s.queryDecoder(`
		{:query {
			:find [id usr usr-id usr-name typ conn verb event-size start-date end-date dlp-count]
			:keys [xt/id session/user session/user-id session/user-name
				   session/type session/connection session/verb session/event-size
				   session/start-date session/end-date session/dlp-count]
			:in [org-id arg-user arg-type arg-conn arg-start-date arg-end-date]
			:where [[a :session/org-id org-id]
					[a :xt/id id]
					[a :session/user usr]
					[a :session/user-id usr-id]
					[a :session/user-name usr-name]
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

func (s *Storage) FindOne(ctx *user.Context, sessionID string) (*Session, error) {
	var resultItems [][]Session
	argUserID := fmt.Sprintf(`"%s"`, ctx.User.Id)
	if ctx.User.IsAdmin() {
		argUserIDBytes, _ := edn.Marshal(nil)
		argUserID = string(argUserIDBytes)
	}
	err := s.queryDecoder(`
	{:query {
		:find [(pull s [:xt/id :session/user :session/user-id :session/user-name
						:session/type :session/connection :session/verb :session/event-size
						:session/start-date :session/end-date :session/dlp-count
						:session/xtdb-stream])]
		:in [org-id arg-session-id arg-user-id]
		:where [[s :session/org-id org-id]
				[s :xt/id arg-session-id]
				[s :session/user-id user-id]
				(or [(= arg-user-id nil)]
                    [(= arg-user-id user-id)])]}
	:in-args [%q %q %v]}`, &resultItems, ctx.Org.Id, sessionID, argUserID)
	if err != nil {
		return nil, err
	}
	items := make([]Session, 0)
	for _, i := range resultItems {
		items = append(items, i[0])
	}
	if len(items) > 0 {
		session := items[0]
		nonIndexedStreams := session.NonIndexedStream["stream"]
		for _, i := range nonIndexedStreams {
			session.EventStream = append(session.EventStream, i)
		}
		return &session, nil
	}
	return nil, nil
}

func (s *Storage) queryDecoder(query string, into any, args ...any) error {
	qs := fmt.Sprintf(query, args...)
	httpBody, err := s.QueryRaw([]byte(qs))
	if err != nil {
		return err
	}
	if strings.Contains(string(httpBody), ":xtdb.error") {
		return fmt.Errorf(string(httpBody))
	}
	return edn.Unmarshal(httpBody, into)
}

// ListAllSessionsID fetches sessions (id,org-id) where start_date > fromDate
func (s *Storage) ListAllSessionsID(fromDate time.Time) ([]*Session, error) {
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
	httpBody, err := s.QueryRaw([]byte(query))
	if err != nil {
		return nil, err
	}
	var sessionList []*Session
	if strings.Contains(string(httpBody), ":xtdb.error") {
		return nil, fmt.Errorf(string(httpBody))
	}
	return sessionList, edn.Unmarshal(httpBody, &sessionList)
}

func (s *Storage) NewGenericStorageWriter() *GenericStorageWriter {
	return &GenericStorageWriter{
		persistFn: s.Persist,
	}
}

func (s *GenericStorageWriter) Write(c plugintypes.Context) error {
	log.Infof("session=%s - saving session, org-id=%v", c.SID, c.OrgID)
	eventStartDate := c.ParamsData.GetTime("start_date")
	if eventStartDate == nil {
		return fmt.Errorf(`missing "start_date" param`)
	}
	sess := &Session{
		ID:               c.SID,
		UserEmail:        c.UserEmail,
		UserID:           c.UserID,
		UserName:         c.UserName,
		Type:             c.ConnectionType,
		Connection:       c.ConnectionName,
		Verb:             c.ClientVerb,
		NonIndexedStream: nil,
		EventSize:        c.ParamsData.Int64("event_size"),
		StartSession:     *eventStartDate,
		EndSession:       c.ParamsData.GetTime("end_time"),
		DlpCount:         c.ParamsData.Int64("dlp_count"),
	}
	eventStreamObj := c.ParamsData.Get("event_stream")
	eventStreamList, _ := eventStreamObj.([]EventStream)
	if eventStreamList != nil {
		nonIndexedEventStream, err := NewNonIndexedEventStreamList(*eventStartDate, eventStreamList...)
		if err != nil {
			return err
		}
		sess.NonIndexedStream = nonIndexedEventStream
	}
	_, err := s.persistFn(&user.Context{Org: &user.Org{Id: c.OrgID}}, sess)
	return err
}
