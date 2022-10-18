package session

import (
	"fmt"
	"log"
	"strings"
	"time"

	pluginscore "github.com/runopsio/hoop/common/plugins/core"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

const (
	OptionUser       SessionOptionKey = "user"
	OptionType       SessionOptionKey = "type"
	OptionConnection SessionOptionKey = "connection"
	OptionStartDate  SessionOptionKey = "start_date"
	OptionEndDate    SessionOptionKey = "end_date"
	OptionOffset     SessionOptionKey = "offset"
	OptionLimit      SessionOptionKey = "limit"

	defaultLimit  int = 100
	defaultOffset int = 0
)

var availableSessionOptions = []SessionOptionKey{
	OptionUser, OptionType, OptionConnection,
	OptionStartDate, OptionEndDate,
	OptionLimit, OptionOffset,
}

type (
	SessionOptionKey string
	Storage          struct {
		*st.Storage
	}
	SessionOption struct {
		optionKey SessionOptionKey
		optionVal any
	}
	GenericStorageWriter struct {
		persistFn func(*user.Context, *Session) (*st.TxResponse, error)
	}
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

func (s *Storage) FindAll(ctx *user.Context, opts ...*SessionOption) (*SessionList, error) {
	inArgsEdn, limit, offset := xtdbQueryParams(ctx.Org.Id, opts...)
	var queryCountResult []any
	if err := s.queryDecoder(`{:query {
		:find [id]
		:keys [xt/id]
		:in [org-id arg-user arg-type arg-conn arg-start-date arg-end-date]
		:where [[a :session/org-id org-id]
				[a :xt/id id]
				[a :session/user usr]
				[a :session/type typ]
				[a :session/connection conn]
				[a :session/start-date start-date]
				(or [(= arg-user nil)]
					[(= usr arg-user)])
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
			:find [id usr typ conn start-date end-date]
			:keys [xt/id session/user session/type session/connection
				   session/start-date session/end-date]
			:in [org-id arg-user arg-type arg-conn arg-start-date arg-end-date]
			:where [[a :session/org-id org-id]
					[a :xt/id id]
					[a :session/user usr]
					[a :session/type typ]
					[a :session/connection conn]
					[a :session/start-date start-date]
					[a :session/start-date end-date]
					(or [(= arg-user nil)]
						[(= usr arg-user)])
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
	var session []Session
	err := s.queryDecoder(`
	{:query {
		:find [s user type connection event-stream start-date end-date]
		:keys [xt/id session/user session/type session/connection
			   session/event-stream session/start-date session/end-date]
		:in [org-id arg-session-id]
		:where [[s :session/org-id org-id]
				[s :xt/id arg-session-id]
				[s :session/user user]
				[s :session/type type]
				[s :session/connection connection]
				[s :session/xtdb-stream xtdb-stream]
				[(get xtdb-stream :stream) event-stream]
				[s :session/start-date start-date]
				[s :session/end-date end-date]]}
	:in-args [%q %q]}`, &session, ctx.Org.Id, sessionID)
	if err != nil {
		return nil, err
	}
	if len(session) > 0 {
		return &session[0], nil
	}
	return nil, fmt.Errorf("session not found")
}

func (s *Storage) queryDecoder(query string, into any, args ...any) error {
	httpBody, err := s.QueryRaw([]byte(fmt.Sprintf(query, args...)))
	if err != nil {
		return err
	}
	if strings.Contains(string(httpBody), ":xtdb.error") {
		return fmt.Errorf(string(httpBody))
	}
	return edn.Unmarshal(httpBody, into)
}

func (s *Storage) NewGenericStorageWriter() *GenericStorageWriter {
	return &GenericStorageWriter{
		persistFn: s.Persist,
	}
}

func (s *GenericStorageWriter) Write(p pluginscore.ParamsData) error {
	log.Printf("writing session=%v, org-id=%v\n", p.Get("session_id"), p.Get("org_id"))
	eventStartDate := p.GetTime("start_date")
	if eventStartDate == nil {
		now := time.Now().UTC()
		eventStartDate = &now
	}
	sess := &Session{
		ID:               p.GetString("session_id"),
		User:             p.GetString("user_id"),
		Type:             p.GetString("connection_type"),
		Connection:       p.GetString("connection_name"),
		NonIndexedStream: nil,
		StartSession:     *eventStartDate,
		EndSession:       p.GetTime("end_time"),
	}
	eventStreamObj := p.Get("event_stream")
	eventStreamList, _ := eventStreamObj.([]EventStream)
	if eventStreamList != nil {
		nonIndexedEventStream, err := NewNonIndexedEventStreamList(*eventStartDate, eventStreamList...)
		if err != nil {
			return err
		}
		sess.NonIndexedStream = nonIndexedEventStream
	}
	_, err := s.persistFn(&user.Context{Org: &user.Org{Id: p.GetString("org_id")}}, sess)
	return err
}
