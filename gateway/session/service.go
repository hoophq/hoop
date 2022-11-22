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
		Persist(context *user.Context, sess *Session) (*st.TxResponse, error)
		FindAll(*user.Context, ...*SessionOption) (*SessionList, error)
		FindOne(context *user.Context, name string) (*Session, error)
		NewGenericStorageWriter() *GenericStorageWriter
	}

	// [time.Time, string, []byte]
	EventStream               []any
	NonIndexedEventStreamList map[edn.Keyword][]EventStream
	Session                   struct {
		ID          string      `json:"id"           edn:"xt/id"`
		OrgID       string      `json:"-"            edn:"session/org-id"`
		User        string      `json:"user"         edn:"session/user"`
		Type        string      `json:"type"         edn:"session/type"`
		Connection  string      `json:"connection"   edn:"session/connection"`
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
