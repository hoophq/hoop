package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/types"
	"slices"
	"time"
)

type IssueTransitionItem struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	IsAvailable bool                  `json:"isAvailable"`
	To          IssueTransitionItemTo `json:"to"`
}

type IssueTransitionItemTo struct {
	Self        string `json:"self"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s IssueTransitionItemTo) String() string {
	return fmt.Sprintf("self=%s,id=%s,name=%s,description=%s", s.Self, s.ID, s.Name, s.Description)
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

type CustomFields map[string]any

type IssueFieldValues[T any] struct {
	CustomFields T `json:"requestFieldValues"`
}

type IssueFields[T any] struct {
	ServiceDeskID string `json:"serviceDeskId"`
	RequestTypeID string `json:"requestTypeId"`
	IsAdfRequest  bool   `json:"isAdfRequest"`

	IssueFieldValues IssueFieldValues[T] `json:"-"`
}

func (A IssueFields[T]) MarshalJSON() ([]byte, error) {
	type ResponseAlias IssueFields[types.Nil]
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

type AqlResponse struct {
	StartAt        int  `json:"startAt"`
	MaxResults     int  `json:"maxResults"`
	Total          int  `json:"total"`
	Last           bool `json:"last"`
	HasMoreResults bool `json:"hasMoreResults"`
	TotalCount     int64

	Values []AqlResponseValue `json:"values"`
}

type AqlResponseValue struct {
	Name       string     `json:"name"`
	GlobalID   string     `json:"globalId"`
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	ObjectKey  string     `json:"objectKey"`
	ObjectType ObjectType `json:"objectType"`
	UpdatedAt  time.Time  `json:"updated"`
	CreatedAt  time.Time  `json:"created"`
}

type ObjectType struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           int    `json:"type"`
	Description    string `json:"description"`
	ObjectSchemaID string `json:"objectSchemaId"`
}

// fieldContexts is the Jira field context page; the context id addresses the
// Assets custom field configuration.
type fieldContexts struct {
	Values []struct {
		ID              string `json:"id"`
		IsGlobalContext bool   `json:"isGlobalContext"`
	} `json:"values"`
}

// assetFieldConfig is the JSM Assets custom field configuration payload.
// Non-Assets fields return the payload with both queries empty.
type assetFieldConfig struct {
	ObjectSchemaID        string `json:"objectSchemaId"`
	ObjectFilterQuery     string `json:"objectFilterQuery"`
	IssueScopeFilterQuery string `json:"issueScopeFilterQuery"`
}

// AssetFieldConfig is the AQL configuration of an Assets custom field:
// ObjectFilterQuery scopes every fetch, IssueScopeFilterQuery may reference
// sibling fields with ${customfield_x.label} placeholders (Jira's native
// dependent-field mechanism). ObjectSchemaID is the schema the field is
// bound to.
type AssetFieldConfig struct {
	JiraField             string
	ObjectSchemaID        string
	ObjectFilterQuery     string
	IssueScopeFilterQuery string
}
