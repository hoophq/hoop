package sessionapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

type sessionParseOption struct {
	withLineBreak bool
	withEventTime bool
	withJsonFmt   bool
	withCsvFmt    bool
	events        []string
}

func toOpenApiSession(s *models.Session) *openapi.Session {
	return &openapi.Session{
		ID:                   s.ID,
		OrgID:                s.OrgID,
		Script:               openapi.SessionScriptType{"data": string(s.BlobInput)},
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
		Review:               topOpenApiReview(s.Review),
		Verb:                 s.Verb,
		Status:               openapi.SessionStatusType(s.Status),
		ExitCode:             s.ExitCode,
		EventStream:          s.BlobStream,
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
		newObj.Items = append(newObj.Items, *toOpenApiSession(&item))
	}
	return newObj
}

func topOpenApiReview(r *models.Review) *openapi.SessionReview {
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

func parseBlobStream(s *models.Session, opts sessionParseOption) (output []byte, err error) {
	if len(s.BlobStream) == 0 {
		return
	}

	var eventStream []any
	if err := json.Unmarshal(s.BlobStream, &eventStream); err != nil {
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
