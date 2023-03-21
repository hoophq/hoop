package session

import (
	"encoding/base64"
	"fmt"
	"time"

	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Persist(ctx *user.Context, sess *Session) (*st.TxResponse, error)
		PersistStatus(sess *SessionStatus) (*st.TxResponse, error)
		EntityHistory(orgID string, sessionID string) ([]SessionStatusHistory, error)
		ValidateSessionID(sessionID string) error
		FindAll(*user.Context, ...*SessionOption) (*SessionList, error)
		FindOne(ctx *user.Context, name string) (*Session, error)
		ListAllSessionsID(startDate time.Time) ([]*Session, error)
		NewGenericStorageWriter() *GenericStorageWriter
	}

	// [time.Time, string, []byte]
	EventStream               []any
	NonIndexedEventStreamList map[edn.Keyword][]EventStream
	Session                   struct {
		ID          string      `json:"id"           edn:"xt/id"`
		OrgID       string      `json:"-"            edn:"session/org-id"`
		UserEmail   string      `json:"user"         edn:"session/user"`
		UserID      string      `json:"user_id"      edn:"session/user-id"`
		UserName    string      `json:"user_name"    edn:"session/user-name"`
		Type        string      `json:"type"         edn:"session/type"`
		Connection  string      `json:"connection"   edn:"session/connection"`
		Verb        string      `json:"verb"         edn:"session/verb"`
		DlpCount    int64       `json:"dlp_count"    edn:"session/dlp_count"`
		EventStream EventStream `json:"event_stream" edn:"session/event-stream"`
		// Must NOT index streams (all top keys are indexed in xtdb)
		NonIndexedStream NonIndexedEventStreamList `json:"-"          edn:"session/xtdb-stream"`
		EventSize        int64                     `json:"event_size" edn:"session/event-size"`
		StartSession     time.Time                 `json:"start_date" edn:"session/start-date"`
		EndSession       *time.Time                `json:"end_date"   edn:"session/end-date"`
	}
	SessionList struct {
		Total       int       `json:"total"`
		HasNextPage bool      `json:"has_next_page"`
		Items       []Session `json:"data"`
	}

	SessionStatus struct {
		ID        string  `json:"id"         edn:"xt/id"`
		SessionID string  `json:"session_id" edn:"session-status/session-id"`
		Phase     string  `json:"phase"      edn:"session-status/phase"`
		Error     *string `json:"error"      edn:"session-status/error"`
	}

	SessionStatusHistory struct {
		TxId   int64         `json:"tx_id"   edn:"xtdb.api/tx-id"`
		TxTime time.Time     `json:"tx_time" edn:"xtdb.api/tx-time"`
		Status SessionStatus `json:"status"  edn:"xtdb.api/doc"`
	}
)

func NewNonIndexedEventStreamList(eventStartDate time.Time, eventStreams ...EventStream) (NonIndexedEventStreamList, error) {
	for idx, ev := range eventStreams {
		if len(ev) != 3 {
			return nil, fmt.Errorf("event stream [%v] in wrong format, accept [time.Time, byte, []byte]", idx)
		}
		eventTime, ok := ev[0].(time.Time)
		if !ok {
			return nil, fmt.Errorf("time in wrong format, expected time.Time, got=%T", ev[0])
		}
		eventTypeByte, _ := ev[1].(byte)
		eventType := string(eventTypeByte)
		if eventType != "o" && eventType != "i" && eventType != "e" {
			return nil, fmt.Errorf(`event-type in wrong format, expected "i", "o" or "e", got=%v`, eventType)
		}
		eventData, ok := ev[2].([]byte)
		if !ok {
			return nil, fmt.Errorf("event-data in wrong format, expected []byte, got=%T", ev[2])
		}

		elapsedTimeInSec := eventTime.Sub(eventStartDate).Seconds()
		eventStreams[idx][0] = elapsedTimeInSec
		eventStreams[idx][1] = eventType
		eventStreams[idx][2] = base64.StdEncoding.EncodeToString(eventData)
	}
	return NonIndexedEventStreamList{
		"stream": eventStreams,
	}, nil
}

func (s *Service) FindOne(context *user.Context, name string) (*Session, error) {
	return s.Storage.FindOne(context, name)
}

func (s *Service) FindAll(ctx *user.Context, opts ...*SessionOption) (*SessionList, error) {
	return s.Storage.FindAll(ctx, opts...)
}

func (s *Service) EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error) {
	if ctx.Org.Id == "" {
		return nil, fmt.Errorf("organization id is empty")
	}
	return s.Storage.EntityHistory(ctx.Org.Id, sessionID)
}

func (s *Service) PersistStatus(status *SessionStatus) (*st.TxResponse, error) {
	return s.Storage.PersistStatus(status)
}

func (s *Service) ValidateSessionID(sessionID string) error {
	return s.Storage.ValidateSessionID(sessionID)
}
