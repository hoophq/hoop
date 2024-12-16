package sessionapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
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
		Connection:           s.Connection,
		Review:               nil, // TODO
		Verb:                 s.Verb,
		Status:               s.Status,
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

func toOpenApiReview(r *types.Review) (newObj *openapi.Review) {
	if r == nil {
		return
	}
	itemGroups := []openapi.ReviewGroup{}
	for _, g := range r.ReviewGroupsData {
		var reviewOwner *openapi.ReviewOwner
		if g.ReviewedBy != nil {
			reviewOwner = &openapi.ReviewOwner{
				ID:      g.ReviewedBy.Id,
				Name:    g.ReviewedBy.Name,
				Email:   g.ReviewedBy.Email,
				SlackID: g.ReviewedBy.SlackID,
			}
		}
		itemGroups = append(itemGroups, openapi.ReviewGroup{
			ID:         g.Id,
			Group:      g.Group,
			Status:     openapi.ReviewRequestStatusType(g.Status),
			ReviewedBy: reviewOwner,
			ReviewDate: g.ReviewDate,
		})
	}
	newObj = &openapi.Review{
		ID:        r.Id,
		OrgId:     r.OrgId,
		CreatedAt: r.CreatedAt,
		Type:      openapi.ReviewType(r.Type),
		Session:   r.Session,
		Input:     r.Input,
		// Redacted for now
		// InputEnvVars:     review.InputEnvVars,
		InputClientArgs: r.InputClientArgs,
		AccessDuration:  r.AccessDuration,
		Status:          openapi.ReviewStatusType(r.Status),
		RevokeAt:        r.RevokeAt,
		ReviewOwner: openapi.ReviewOwner{
			ID:      r.ReviewOwner.Id,
			Name:    r.ReviewOwner.Name,
			Email:   r.ReviewOwner.Email,
			SlackID: r.ReviewOwner.SlackID,
		},
		Connection: openapi.ReviewConnection{
			ID:   r.Connection.Id,
			Name: r.Connection.Name,
		},
		ReviewGroupsData: itemGroups,
	}
	return
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
