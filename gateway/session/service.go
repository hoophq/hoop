package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgreview "github.com/runopsio/hoop/gateway/pgrest/review"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Service struct {
		Storage storage
	}

	storage interface {
		// Persist(ctx *user.Context, sess *types.Session) (*st.TxResponse, error)
		// PersistStatus(sess *SessionStatus) (*st.TxResponse, error)
		// EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error)
		// ValidateSessionID(sessionID string) error
		FindAll(*user.Context, ...*SessionOption) (*SessionList, error)
		FindOne(ctx *user.Context, name string) (*types.Session, error)
		ListAllSessionsID(startDate time.Time) ([]*types.Session, error)
		FindReviewBySessionID(ctx *user.Context, sessionID string) (*types.Review, error)
		PersistReview(ctx *user.Context, review *types.Review) (int64, error)
	}

	// [time.Time, string, []byte]
	SessionList struct {
		Total       int             `json:"total"`
		HasNextPage bool            `json:"has_next_page"`
		Items       []types.Session `json:"data"`
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

// func NewNonIndexedEventStreamList(eventStartDate time.Time, eventStreams ...types.SessionEventStream) (types.SessionNonIndexedEventStreamList, error) {
// 	for idx, ev := range eventStreams {
// 		if len(ev) != 3 {
// 			return nil, fmt.Errorf("event stream [%v] in wrong format, accept [time.Time, byte, []byte]", idx)
// 		}
// 		eventTime, ok := ev[0].(time.Time)
// 		if !ok {
// 			return nil, fmt.Errorf("time in wrong format, expected time.Time, got=%T", ev[0])
// 		}
// 		eventTypeByte, _ := ev[1].(byte)
// 		eventType := string(eventTypeByte)
// 		if eventType != "o" && eventType != "i" && eventType != "e" {
// 			return nil, fmt.Errorf(`event-type in wrong format, expected "i", "o" or "e", got=%v`, eventType)
// 		}
// 		eventData, ok := ev[2].([]byte)
// 		if !ok {
// 			return nil, fmt.Errorf("event-data in wrong format, expected []byte, got=%T", ev[2])
// 		}

// 		elapsedTimeInSec := eventTime.Sub(eventStartDate).Seconds()
// 		eventStreams[idx][0] = elapsedTimeInSec
// 		eventStreams[idx][1] = eventType
// 		eventStreams[idx][2] = base64.StdEncoding.EncodeToString(eventData)
// 	}
// 	return types.SessionNonIndexedEventStreamList{
// 		"stream": eventStreams,
// 	}, nil
// }

func (s *Service) FindReviewBySessionID(ctx *user.Context, sessionID string) (*types.Review, error) {
	return s.Storage.FindReviewBySessionID(ctx, sessionID)
}

func (s *Service) FindOne(context *user.Context, name string) (*types.Session, error) {
	return s.Storage.FindOne(context, name)
}

func (s *Service) FindAll(ctx *user.Context, opts ...*SessionOption) (*SessionList, error) {
	return s.Storage.FindAll(ctx, opts...)
}

// func (s *Service) EntityHistory(ctx *user.Context, sessionID string) ([]SessionStatusHistory, error) {
// 	if ctx.Org.Id == "" {
// 		return nil, fmt.Errorf("organization id is empty")
// 	}
// 	return s.Storage.EntityHistory(ctx, sessionID)
// }

// func (s *Service) PersistStatus(status *SessionStatus) (*st.TxResponse, error) {
// 	return s.Storage.PersistStatus(status)
// }

// func (s *Service) ValidateSessionID(sessionID string) error {
// 	return s.Storage.ValidateSessionID(sessionID)
// }

func (s *Service) PersistReview(context *user.Context, review *types.Review) error {
	if pgrest.Rollout {
		// the usage of this function is only to update the status of the review
		return pgreview.New().PatchStatus(context, review.Id, review.Status)
	}
	if review.Id == "" {
		review.Id = uuid.NewString()
	}

	for i, r := range review.ReviewGroupsData {
		if r.Id == "" {
			review.ReviewGroupsData[i].Id = uuid.NewString()
		}
	}

	if _, err := s.Storage.PersistReview(context, review); err != nil {
		return err
	}
	return nil
}

type sessionParseOption struct {
	withLineBreak bool
	withEventTime bool
	withJsonFmt   bool
	withCsvFmt    bool
	events        []string
}

func parseSessionToFile(s *types.Session, opts sessionParseOption) (output []byte) {
	var jsonEventStreamList []map[string]string
	for _, eventList := range s.EventStream {
		event := eventList.(types.SessionEventStream)
		eventTime, _ := event[0].(float64)
		eventType, _ := event[1].(string)
		eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))
		if !slices.Contains(opts.events, eventType) {
			continue
		}
		if opts.withJsonFmt {
			jsonEventStreamList = append(jsonEventStreamList, map[string]string{
				"time":   s.StartSession.Add(time.Second * time.Duration(eventTime)).Format(time.RFC3339),
				"type":   eventType,
				"stream": string(eventData),
			})
			continue
		}
		if opts.withEventTime {
			eventTime := s.StartSession.Add(time.Second * time.Duration(eventTime)).Format(time.RFC3339)
			eventTime = fmt.Sprintf("%v ", eventTime)
			output = append(output, []byte(eventTime)...)
		}
		switch eventType {
		case "i":
			output = append(output, eventData...)
		case "o", "e":
			output = append(output, eventData...)
		}
		if opts.withLineBreak {
			output = append(output, '\n')
		}
		if opts.withCsvFmt {
			output = bytes.ReplaceAll(output, []byte("\t"), []byte(`,`))
		}
	}
	if opts.withJsonFmt {
		output, _ = json.Marshal(jsonEventStreamList)
	}
	return
}
