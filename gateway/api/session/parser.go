package sessionapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/pgtypes"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

var errEventStreamUnsupportedFormat = errors.New("event_stream attribute has an unknown type format")

type sessionParseOption struct {
	withLineBreak bool
	withEventTime bool
	withJsonFmt   bool
	withCsvFmt    bool
	events        []string
}

func toOpenApiSession(s *models.Session, hasInputExpanded bool) *openapi.Session {
	var blobStream json.RawMessage
	if s.BlobStream != nil {
		blobStream = s.BlobStream.BlobStream
	}
	var blobInputStream openapi.SessionScriptType
	if hasInputExpanded {
		blobInputStream = openapi.SessionScriptType{"data": string(s.BlobInput)}
	}

	return &openapi.Session{
		ID:                   s.ID,
		OrgID:                s.OrgID,
		Script:               blobInputStream,
		ScriptSize:           s.BlobInputSize,
		Labels:               s.Labels,
		IntegrationsMetadata: s.IntegrationsMetadata,
		Metadata:             s.Metadata,
		Metrics:              s.Metrics,
		UserEmail:            s.UserEmail,
		UserID:               s.UserID,
		UserName:             s.UserName,
		Type:                 s.ConnectionType,
		ConnectionSubtype:    s.ConnectionSubtype,
		Connection:           s.Connection,
		ConnectionTags:       s.ConnectionTags,
		Review:               topOpenApiReview(s.Review),
		Verb:                 s.Verb,
		Status:               openapi.SessionStatusType(s.Status),
		ExitCode:             s.ExitCode,
		EventStream:          blobStream,
		EventSize:            s.BlobStreamSize,
		StartSession:         s.CreatedAt,
		EndSession:           s.EndSession,
	}
}

func toOpenApiSessionList(s *models.SessionList) *openapi.SessionList {
	newObj := &openapi.SessionList{
		Total:       s.Total,
		HasNextPage: s.HasNextPage,
		Items:       []openapi.Session{},
	}
	for _, item := range s.Items {
		item.BlobInput = "" // not displayed when listing
		newObj.Items = append(newObj.Items, *toOpenApiSession(&item, false))
	}
	return newObj
}

func topOpenApiReview(r *models.SessionReview) *openapi.SessionReview {
	if r == nil {
		return nil
	}
	itemGroups := []openapi.ReviewGroup{}
	for _, rg := range r.ReviewGroups {
		var reviewOwner *openapi.ReviewOwner
		if rg.OwnerID != nil {
			reviewOwner = &openapi.ReviewOwner{
				ID:      ptr.ToString(rg.OwnerID),
				Name:    ptr.ToString(rg.OwnerName),
				Email:   ptr.ToString(rg.OwnerEmail),
				SlackID: ptr.ToString(rg.OwnerSlackID),
			}
		}
		itemGroups = append(itemGroups, openapi.ReviewGroup{
			ID:         rg.ID,
			Group:      rg.GroupName,
			Status:     openapi.ReviewRequestStatusType(rg.Status),
			ReviewedBy: reviewOwner,
			ReviewDate: rg.ReviewedAt,
		})
	}
	return &openapi.SessionReview{
		ID:   r.ID,
		Type: openapi.ReviewType(r.Type),
		// this attribute is saved as seconds
		// but we keep compatibility with clients to show as nano seconds
		AccessDuration:   time.Duration(r.AccessDurationSec) * time.Second,
		Status:           openapi.ReviewStatusType(r.Status),
		CreatedAt:        r.CreatedAt,
		RevokeAt:         r.RevokedAt,
		ReviewGroupsData: itemGroups,
	}
}

// encode the blob stream based on the format type
func encodeBlobStream(s *models.Session, format openapi.SessionEventStreamType) error {
	if s.BlobStream == nil {
		return nil
	}
	switch format {
	case openapi.SessionEventStreamUTF8Type, openapi.SessionEventStreamBase64Type:
		output, err := parseBlobStream(s, sessionParseOption{events: []string{"o", "e"}})
		if err != nil {
			return err
		}
		s.BlobStream.BlobStream = json.RawMessage(fmt.Sprintf(`[%q]`, string(output)))
		s.BlobStreamSize = int64(utf8.RuneCountInString(string(output)))
		if format == "base64" {
			encOutput := base64.StdEncoding.EncodeToString(output)
			s.BlobStream.BlobStream = json.RawMessage(fmt.Sprintf(`[%q]`, encOutput))
			s.BlobStreamSize = int64(len(encOutput))
		}
	case openapi.SessionEventStreamRawQueriesType:
		// It maintains compatibility with older sessions
		// that are not stored in wire protocol format.
		if !s.BlobStream.IsWireProtocol() {
			return nil
		}
		blobStream, blobSize, err := parseRawQueries(
			s.BlobStream.BlobStream,
			proto.ToConnectionType(s.ConnectionType, s.ConnectionSubtype),
		)
		if err != nil {
			return err
		}
		s.BlobStream.BlobStream = blobStream
		s.BlobStreamSize = blobSize
	default:
		return errEventStreamUnsupportedFormat
	}
	return nil
}

func parseBlobStream(s *models.Session, opts sessionParseOption) (output []byte, err error) {
	if s.BlobStream == nil || len(s.BlobStream.BlobStream) == 0 {
		return
	}

	var eventStream []any
	if err := json.Unmarshal(s.BlobStream.BlobStream, &eventStream); err != nil {
		return nil, fmt.Errorf("failed decoding blob stream: %v", err)
	}

	var jsonEventStreamList []map[string]string
	for _, eventList := range eventStream {
		event := eventList.([]any)
		eventTime, _ := event[0].(float64)
		eventType, _ := event[1].(string)
		eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))
		if !slices.Contains(opts.events, eventType) {
			continue
		}
		if opts.withJsonFmt {
			jsonEventStreamList = append(jsonEventStreamList, map[string]string{
				"time":   s.CreatedAt.Add(time.Second * time.Duration(eventTime)).Format(time.RFC3339),
				"type":   eventType,
				"stream": string(eventData),
			})
			continue
		}
		if opts.withEventTime {
			eventTime := s.CreatedAt.Add(time.Second * time.Duration(eventTime)).Format(time.RFC3339)
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

func parseRawQueries(blobStream json.RawMessage, connProtoType proto.ConnectionType) (json.RawMessage, int64, error) {
	if connProtoType != proto.ConnectionTypePostgres || len(blobStream) == 0 {
		return nil, 0, nil
	}
	var in []any
	if err := json.Unmarshal(blobStream, &in); err != nil {
		return nil, 0, fmt.Errorf("failed decoding blob stream: %v", err)
	}

	var blobSize int64
	var out []any
	for _, eventList := range in {
		event := eventList.([]any)
		eventTime, _ := event[0].(float64)
		eventType, _ := event[1].(string)
		if eventType != "i" {
			continue
		}
		eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))
		queryBytes := pgtypes.ParseQuery(eventData)
		if len(queryBytes) == 0 {
			continue
		}
		blobSize += int64(len(queryBytes))
		out = append(out, []any{eventTime, eventType, base64.StdEncoding.EncodeToString(queryBytes)})
	}
	rawQueries, err := json.Marshal(out)
	return rawQueries, blobSize, err
}
