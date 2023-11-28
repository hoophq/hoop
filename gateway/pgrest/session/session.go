package pgsession

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type session struct{}

func New() *session { return &session{} }

func (s *session) UpdateStatus(ctx pgrest.OrgContext, sessionID, status string) error {
	return pgrest.New("/sessions?org_id=eq.%s&id=eq.%s", ctx.GetOrgID(), sessionID).
		Patch(map[string]any{"status": status}).
		Error()
}

func (s *session) Upsert(ctx pgrest.OrgContext, sess types.Session) (err error) {
	switch sess.Status {
	// this will be executed in distinct flows
	case types.SessionStatusOpen:
		// generate deterministic uuid based on the session id to avoid duplicates
		blobInputID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("blobinput:%s", sess.ID)))
		defer func() {
			if err != nil {
				return
			}
			if input := sess.Script["data"]; input != "" {
				err = pgrest.New("/blobs?on_conflict=org_id,id").Upsert(map[string]any{
					"id":          blobInputID,
					"org_id":      sess.OrgID,
					"type":        "session-input",
					"blob_stream": []any{input},
				}).Error()
			}
		}()
		err = pgrest.New("/sessions").Upsert(map[string]any{
			"id":              sess.ID,
			"org_id":          sess.OrgID,
			"labels":          sess.Labels,
			"connection":      sess.Connection,
			"connection_type": sess.Type,
			"verb":            sess.Verb,
			"user_id":         sess.UserID,
			"user_name":       sess.UserName,
			"user_email":      sess.UserEmail,
			"blob_input_id":   blobInputID,
			"status":          sess.Status,
		}).Error()
	case types.SessionStatusDone:
		blobStreamID := uuid.NewString()
		defer func() {
			if err != nil {
				return
			}
			err = pgrest.New("/blobs").Create(map[string]any{
				"id":          blobStreamID,
				"org_id":      sess.OrgID,
				"type":        "session-stream",
				"blob_stream": sess.NonIndexedStream["stream"],
			}).Error()
		}()
		err = pgrest.New("/sessions?org_id=eq.%s&id=eq.%s", sess.OrgID, sess.ID).Patch(map[string]any{
			"labels":         sess.Labels,
			"blob_stream_id": blobStreamID,
			"status":         sess.Status,
			"ended_at":       sess.EndSession.Format(time.RFC3339Nano),
			"metadata": map[string]any{
				"redact_count": sess.DlpCount,
			},
		}).Error()
	default:
		return fmt.Errorf("unknown session status %q", sess.Status)
	}
	return
}

func (s *session) FetchAll(ctx pgrest.OrgContext, opts ...*pgrest.SessionOption) (*pgrest.SessionList, error) {
	var items []pgrest.Session
	qs, limit := toQueryParams(ctx.GetOrgID(), opts...)
	err := pgrest.New("/sessions?%s", qs).List().DecodeInto(&items)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return &pgrest.SessionList{}, nil
		}
		return nil, err
	}
	total := pgrest.New("/sessions?%s", qs).ExactCount()
	if total == -1 {
		return nil, fmt.Errorf("failed performing counting session items")
	}
	return &pgrest.SessionList{
		Total:       total,
		HasNextPage: len(items) == limit,
		Items:       items,
	}, nil
}

func (s *session) FetchOne(ctx pgrest.OrgContext, sessionID string) (*types.Session, error) {
	var sess pgrest.Session
	err := pgrest.New("/sessions?select=*,blob_input(id,org_id,type,type,size,blob_stream),blob_stream(id,org_id,type,size,blob_stream)&org_id=eq.%s&id=eq.%s",
		ctx.GetOrgID(), sessionID).
		FetchOne().
		DecodeInto(&sess)
	if err != nil {
		if err == pgrest.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}

	blobStream, blobStreamSize := sess.GetBlobStream()
	return &types.Session{
		ID:               sess.ID,
		OrgID:            sess.OrgID,
		Script:           types.SessionScript{"data": sess.GetBlobInput()},
		Labels:           sess.Labels,
		UserEmail:        sess.UserEmail,
		UserID:           sess.UserID,
		UserName:         sess.UserName,
		Type:             sess.ConnectionType,
		Connection:       sess.Connection,
		Verb:             sess.Verb,
		Status:           sess.Status,
		DlpCount:         sess.GetRedactCount(),
		EventStream:      nil,
		NonIndexedStream: types.SessionNonIndexedEventStreamList{"stream": blobStream},
		EventSize:        blobStreamSize,
		StartSession:     sess.GetCreatedAt(),
		EndSession:       sess.GetEndedAt(),
	}, nil
}

func (s *session) FetchAllFromDate(fromDate time.Time) ([]*types.Session, error) {
	var sessionList []*types.Session
	err := pgrest.New("/sessions?select=id,org_id&created_at=gt.%s", fromDate.Format(time.RFC3339)).List().DecodeInto(&sessionList)
	if err != nil && err != pgrest.ErrNotFound {
		return nil, err
	}
	return sessionList, nil
}

func toQueryParams(orgID string, opts ...*pgrest.SessionOption) (string, int) {
	vals := url.Values{}
	limit := pgrest.DefaultLimit
	vals.Set("org_id", fmt.Sprintf("eq.%s", orgID))
	vals.Set("limit", fmt.Sprintf("%v", limit))
	vals.Set("offset", fmt.Sprintf("%v", pgrest.DefaultOffset))
	vals.Set("order", "created_at.desc")
	for _, opt := range opts {
		val := fmt.Sprintf("%v", opt.OptionVal)
		switch opt.OptionKey {
		case pgrest.OptionUser:
			vals.Set("user_id", fmt.Sprintf("eq.%s", val))
		case pgrest.OptionType:
			vals.Set("connection_type", fmt.Sprintf("eq.%s", val))
		case pgrest.OptionConnection:
			vals.Set("connection", fmt.Sprintf("eq.%s", val))
		case pgrest.OptionStartDate:
			if t, ok := opt.OptionVal.(time.Time); ok {
				vals.Add("created_at", fmt.Sprintf("gt.%s", t.Format(time.RFC3339)))
			}
		case pgrest.OptionEndDate:
			if t, ok := opt.OptionVal.(time.Time); ok {
				vals.Add("created_at", fmt.Sprintf("lt.%s", t.Format(time.RFC3339)))
			}
		case pgrest.OptionLimit:
			optLimit, _ := strconv.Atoi(val)
			if optLimit > pgrest.DefaultLimit {
				vals.Set("limit", fmt.Sprintf("%v", pgrest.DefaultLimit))
				break
			}
			limit = optLimit
			vals.Set(string(opt.OptionKey), val)
		case pgrest.OptionOffset:
			vals.Set(string(opt.OptionKey), val)
		}
	}

	return encodeUrlVals(vals, map[string]any{"created_at": nil}), limit
}

func encodeUrlVals(v url.Values, skipEscape map[string]any) string {
	if v == nil {
		return ""
	}
	var buf strings.Builder
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vs := v[k]
		keyEscaped := url.QueryEscape(k)
		for _, v := range vs {
			if buf.Len() > 0 {
				buf.WriteByte('&')
			}
			buf.WriteString(keyEscaped)
			buf.WriteByte('=')
			if _, ok := skipEscape[k]; !ok {
				v = url.QueryEscape(v)
			}
			buf.WriteString(v)
		}
	}
	return buf.String()
}
