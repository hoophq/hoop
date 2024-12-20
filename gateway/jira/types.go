package jira

import (
	"bytes"
	"encoding/json"
	"go/types"
	"slices"
	"time"

	storagev2types "github.com/hoophq/hoop/gateway/storagev2/types"
)

type IssueTransitionItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsAvailable bool   `json:"isAvailable"`
}

type IssueTransition struct {
	Expand string                `json:"expand"`
	Items  []IssueTransitionItem `json:"transitions"`
}

// https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-post
type IssueResponse struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Self string `json:"self"`
}

type RequestLinks struct {
	JiraRest string `json:"jiraRest"`
	Web      string `json:"web"`
	Agent    string `json:"agent"`
	Self     string `json:"self"`
}

type RequestResponse struct {
	IssueID  string       `json:"issueId"`
	IssueKey string       `json:"issueKey"`
	Links    RequestLinks `json:"_links"`
}

type ServiceDeskValue struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ProjectKey  string `json:"projectKey"`
}

type ServiceDesk struct {
	Start      int                `json:"start"`
	Size       int                `json:"size"`
	Limit      int                `json:"limit"`
	IsLastPage bool               `json:"isLastPage"`
	Values     []ServiceDeskValue `json:"values"`
}

type Project struct {
	Key string `json:"key"`
}

type Issuetype struct {
	Name string `json:"name"`
}

type CustomFields map[string]any

type IssueFields[T any] struct {
	Project   Project   `json:"project"`
	Summary   string    `json:"summary"`
	Issuetype Issuetype `json:"issuetype"`

	CustomFields T `json:"-"`
}

func (A IssueFields[T]) MarshalJSON() ([]byte, error) {
	type ResponseAlias IssueFields[types.Nil]
	resp, err := json.Marshal(ResponseAlias{
		Project:   A.Project,
		Summary:   A.Summary,
		Issuetype: A.Issuetype,
	})
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(A.CustomFields)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(data, []byte(`{}`)) {
		return resp, nil
	}
	v := append(resp[1:len(resp)-1], byte(','))
	resp = slices.Insert(data, 1, v...)
	return resp, nil
}

type IssueFieldValues[T any] struct {
	CustomFields T `json:"requestFieldValues"`
}

type IssueFieldsV2[T any] struct {
	ServiceDeskID string `json:"serviceDeskId"`
	RequestTypeID string `json:"requestTypeId"`
	IsAdfRequest  bool   `json:"isAdfRequest"`

	IssueFieldValues IssueFieldValues[T] `json:"-"`
}

func (A IssueFieldsV2[T]) MarshalJSON() ([]byte, error) {
	type ResponseAlias IssueFieldsV2[types.Nil]
	resp, err := json.Marshal(ResponseAlias{
		ServiceDeskID: A.ServiceDeskID,
		RequestTypeID: A.RequestTypeID,
		IsAdfRequest:  A.IsAdfRequest,
	})
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(A.IssueFieldValues)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(data, []byte(`{}`)) {
		return resp, nil
	}
	v := append(resp[1:len(resp)-1], byte(','))
	resp = slices.Insert(data, 1, v...)
	return resp, nil
}

func loadDefaultPresetFields(s storagev2types.Session) map[string]string {
	return map[string]string{
		"session.id":         s.ID,
		"session.user_email": s.UserEmail,
		"session.user_id":    s.UserID,
		"session.user_name":  s.UserName,
		"session.type":       s.Type,
		// "session.subtype":    "",
		"session.connection": s.Connection,
		"session.status":     s.Status,
		"session.verb":       s.Verb,
		"session.start_date": s.StartSession.Format(time.RFC3339),
	}
}
