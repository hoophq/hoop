package session

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	st "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/user"
	"olympos.io/encoding/edn"
)

type (
	Service struct {
		Storage storage
	}

	Review struct {
		Id             string        `json:"id"                      edn:"xt/id"`
		Type           string        `json:"type"                    edn:"review/type"`
		Session        string        `json:"session"                 edn:"review/session"`
		Input          string        `json:"input"                   edn:"review/input"`
		AccessDuration time.Duration `json:"access_duration"         edn:"review/access-duration"`
		Status         Status        `json:"status"                  edn:"review/status"`
		RevokeAt       *time.Time    `json:"revoke_at"               edn:"review/revoke-at"`
		CreatedBy      Owner         `json:"created_by"              edn:"review/created-by"`
		Connection     Connection    `json:"connection"              edn:"review/connection"`
		ReviewGroups   []Group       `json:"review_groups,omitempty" edn:"review/review-groups"`
	}

	Owner struct {
		Id    string `json:"id,omitempty"   edn:"xt/id"`
		Name  string `json:"name,omitempty" edn:"user/name"`
		Email string `json:"email"          edn:"user/email"`
	}

	Connection struct {
		Id   string `json:"id,omitempty" edn:"xt/id"`
		Name string `json:"name"         edn:"connection/name"`
	}

	Group struct {
		Id         string  `json:"id"          edn:"xt/id"`
		Group      string  `json:"group"       edn:"review-group/group"`
		Status     Status  `json:"status"      edn:"review-group/status"`
		ReviewedBy *Owner  `json:"reviewed_by" edn:"review-group/reviewed-by"`
		ReviewDate *string `json:"review_date" edn:"review-group/review_date"`
	}

	Status string

	storage interface {
		Persist(ctx *user.Context, sess *Session) (*st.TxResponse, error)
		PersistStatus(sess *SessionStatus) (*st.TxResponse, error)
		EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error)
		ValidateSessionID(sessionID string) error
		FindAll(*user.Context, ...*SessionOption) (*SessionList, error)
		FindOne(ctx *user.Context, name string) (*Session, error)
		ListAllSessionsID(startDate time.Time) ([]*Session, error)
		NewGenericStorageWriter() *GenericStorageWriter
		FindReviewBySessionID(sessionID string) (*Review, error)
		PersistReview(ctx *user.Context, review *Review) (int64, error)
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
		Status      any         `json:"status"       edn:"session/status"`
		DlpCount    int64       `json:"dlp_count"    edn:"session/dlp-count"`
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

func (s *Service) FindReviewBySessionID(sessionID string) (*Review, error) {
	return s.Storage.FindReviewBySessionID(sessionID)
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
	return s.Storage.EntityHistory(ctx, sessionID)
}

func (s *Service) PersistStatus(status *SessionStatus) (*st.TxResponse, error) {
	return s.Storage.PersistStatus(status)
}

func (s *Service) ValidateSessionID(sessionID string) error {
	return s.Storage.ValidateSessionID(sessionID)
}

func (s *Service) PersistReview(context *user.Context, review *Review) error {
	if review.Id == "" {
		review.Id = uuid.NewString()
	}

	for i, r := range review.ReviewGroups {
		if r.Id == "" {
			review.ReviewGroups[i].Id = uuid.NewString()
		}
	}

	if _, err := s.Storage.PersistReview(context, review); err != nil {
		return err
	}
	return nil
}
