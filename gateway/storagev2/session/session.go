package sessionstorage

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

func Put(ctx *storagev2.Context, s types.Session) (err error) {
	switch s.Status {
	case types.SessionStatusOpen:
		blobInputID := uuid.NewString()
		defer func() {
			if err != nil {
				return
			}
			err = pgrest.New("/blobs").Create(map[string]any{
				"id":          blobInputID,
				"org_id":      s.OrgID,
				"type":        "session-input",
				"blob_stream": []any{s.Script},
			}).Error()
		}()
		err = pgrest.New("/sessions").Create(map[string]any{
			"id":              s.ID,
			"org_id":          s.OrgID,
			"labels":          s.Labels,
			"connection":      s.Connection,
			"connection_type": s.Type,
			"verb":            s.Verb,
			"user_id":         s.UserID,
			"user_name":       s.UserName,
			"user_email":      s.UserEmail,
			"blob_input_id":   blobInputID,
			"status":          s.Status,
		}).Error()
	case types.SessionStatusDone:
		blobStreamID := uuid.NewString()
		defer func() {
			if err != nil {
				return
			}
			err = pgrest.New("/blobs").Create(map[string]any{
				"id":          blobStreamID,
				"org_id":      s.OrgID,
				"type":        "session-stream",
				"blob_stream": s.NonIndexedStream["stream"],
			}).Error()
		}()
		err = pgrest.New("/sessions?org_id=eq.%s&id=eq.%s", s.OrgID, s.ID).Patch(map[string]any{
			"labels":         s.Labels,
			"blob_stream_id": blobStreamID,
			"status":         s.Status,
			"ended_at":       s.EndSession.Format(time.RFC3339Nano),
			"metadata": map[string]any{
				"redact_count": s.DlpCount,
			},
		}).Error()
	default:
		return fmt.Errorf("unknown session status %q", s.Status)
	}
	return
	// _, err := storage.Put(session)
	// return err
}

func FindOne(ctx *storagev2.Context, sessionID string) (*types.Session, error) {
	var s pgrest.Session
	err := pgrest.New("/sessions?select=*,blob_input(id,org_id,type,type,size,blob_stream),blob_stream(id,org_id,type,size,blob_stream)&org_id=eq.%s&id=eq.%s",
		ctx.OrgID, sessionID).
		FetchOne().
		DecodeInto(&s)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	blobStream, blobStreamSize := s.GetBlobStream()
	return &types.Session{
		ID:               s.ID,
		OrgID:            s.OrgID,
		Script:           types.SessionScript{"data": s.GetBlobInput()},
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
		NonIndexedStream: types.SessionNonIndexedEventStreamList{"stream": blobStream},
		EventSize:        blobStreamSize,
		StartSession:     s.GetCreatedAt(),
		EndSession:       s.GetEndedAt(),
	}, nil
	// return nil, nil
	// payload := fmt.Sprintf(`{:query {
	// 	:find [(pull ?session [*])]
	// 	:in [org-id session-id user-id]
	// 	:where [[?session :session/org-id org-id]
	//       	[?session :xt/id session-id]
	// 					[?session :session/user-id user-id]]}
	// 	:in-args [%q %q %q]}`, storageCtx.OrgID, sessionID, storageCtx.UserID)

	// b, err := storageCtx.Query(payload)
	// if err != nil {
	// 	return nil, err
	// }

	// var sessions [][]types.Session
	// if err := edn.Unmarshal(b, &sessions); err != nil {
	// 	return nil, err
	// }

	// if len(sessions) > 0 {
	// 	return &sessions[0][0], nil
	// }

	// return nil, nil
}
