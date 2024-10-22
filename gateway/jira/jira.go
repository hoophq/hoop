package jira

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
	sessionstorage "github.com/hoophq/hoop/gateway/storagev2/session"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

type orgContext struct {
	orgID string
}

func (oc orgContext) GetOrgID() string {
	return oc.orgID
}

type sessionParseOption struct {
	withLineBreak bool
	events        []string
}

func parseSessionToFile(s *types.Session, opts sessionParseOption) []byte {
	output := []byte{}
	for _, eventList := range s.EventStream {
		event := eventList.(types.SessionEventStream)
		eventType, _ := event[1].(string)
		eventData, _ := base64.StdEncoding.DecodeString(event[2].(string))

		if !slices.Contains(opts.events, eventType) {
			continue
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
	}

	return output
}

func CreateIssue(orgId, summary, issueType, sessionID string) error {
	dbJiraIntegration, err := models.GetJiraIntegration(orgId)
	if err != nil {
		log.Warnf("Failed to get Jira integration: %v", err)
		return fmt.Errorf("failed to get Jira integration: %w", err)
	}
	if dbJiraIntegration == nil {
		log.Warnf("No Jira integration found for org_id: %s", orgId)
		return fmt.Errorf("no Jira integration found")
	}

	if dbJiraIntegration.Status != models.JiraIntegrationStatusActive {
		return fmt.Errorf("jira integration is not active for org_id: %s", orgId)
	}

	jiraCtx := orgContext{orgID: orgId}
	sessiondb := pgsession.New()

	session, err := sessiondb.FetchOne(jiraCtx, sessionID)
	if err != nil {
		return fmt.Errorf("fetch session error: %v", err)
	}

	sessionScriptLength := len(session.Script["data"])
	if sessionScriptLength > 1000 {
		sessionScriptLength = 1000
	}

	content := CreateSessionJiraIssueTemplate{
		UserName:       session.UserName,
		ConnectionName: session.Connection,
		SessionID:      sessionID,
		SessionScript:  session.Script["data"][0:sessionScriptLength],
	}

	issue := createSessionJiraIssueTemplate(dbJiraIntegration.ProjectKey, summary, issueType, content)
	body, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("error serializing issue: %v", err)
	}

	// Create the request to JIRA
	req, err := createJiraRequest(orgId, "POST", "/rest/api/3/issue", body)
	if err != nil {
		return err
	}

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	// Read the response
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create issue: %s", string(respBody))
	}

	log.Infof("Issue created successfully!")
	log.Infof("Updating session")

	// Parse the response to get the issue key
	var issueResponse struct {
		Key string `json:"key"`
	}

	if err := json.Unmarshal(respBody, &issueResponse); err != nil {
		return fmt.Errorf("error parsing issue response: %v", err)
	}

	if err := sessiondb.UpdateJiraIssue(orgId, content.SessionID, issueResponse.Key); err != nil {
		return fmt.Errorf("error updating session with Jira issue: %v", err)
	}

	log.Infof("Session updated with Jira issue key: %s", issueResponse.Key)
	return nil
}

// Function to get the current issue description
func getIssueDescription(orgId, issueKey string) (map[string]interface{}, error) {
	req, err := createJiraRequest(orgId, "GET", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request to get issue: %v", err)
	}

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	// Check if the response was successful
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch issue description, status: %d", resp.StatusCode)
	}

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}

	// Parse the JSON response to get the description
	var issueData map[string]interface{}
	if err := json.Unmarshal(body, &issueData); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %v", err)
	}

	// Access the issue fields and description
	fields, ok := issueData["fields"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unable to access issue fields")
	}

	description, ok := fields["description"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unable to access issue description")
	}

	return description, nil
}

func sendJiraIssueUpdate(orgID, issueKey string, issue map[string]interface{}) error {
	payloadBytes, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to serialize update payload: %v", err)
	}

	req, err := createJiraRequest(orgID, "PUT", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), payloadBytes)
	if err != nil {
		return fmt.Errorf("failed to create request for updating issue: %v", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send update request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update issue description. Status: %s, Body: %s", resp.Status, string(respBody))
	}

	log.Infof("Issue description updated successfully!")
	return nil
}

func UpdateJiraIssueContent(actionType, orgId, sessionID string, newInfo ...interface{}) error {
	err := hasJiraIntegrationEnabled(orgId)
	if err != nil {
		return fmt.Errorf("jira integration is not enabled, reason: %v", err)
	}

	jiraCtx := orgContext{orgID: orgId}
	session, err := sessionstorage.FindOne(jiraCtx, sessionID)
	if err != nil {
		return fmt.Errorf("fetch session error: %v", err)
	}

	currentDescription, err := getIssueDescription(orgId, session.JiraIssue)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	contentList, ok := currentDescription["content"].([]interface{})
	if !ok {
		return fmt.Errorf("failed to assert content as []interface{}")
	}

	var issue map[string]interface{}

	switch actionType {
	case "add-create-review":
		newInfo := AddCreateReviewIssueTemplate{
			ApiURL:    appconfig.Get().ApiURL(),
			SessionID: sessionID,
		}

		issue = updateReviewJiraIssueTemplate(contentList, newInfo)
	case "add-review-status":
		issue = addNewReviewByUserIssueTemplate(contentList, newInfo[0].(AddNewReviewByUserIssueTemplate))
	case "add-review-rejected", "add-review-revoked":
		newInfo := "rejected"
		if actionType != "add-review-rejected" {
			newInfo = "revoked"
		}

		issue = addReviewRejectedOrRevokedIssueTemplate(contentList, newInfo)
	case "add-review-ready":
		newInfo := AddReviewReadyIssueTemplate{
			ApiURL:    appconfig.Get().ApiURL(),
			SessionID: sessionID,
		}

		issue = addReviewReadyIssueTemplate(contentList, newInfo)
	case "add-session-executed":
		events := []string{"o", "e"}
		payload := parseSessionToFile(session, sessionParseOption{withLineBreak: true, events: events})

		payloadLength := len(payload)
		if payloadLength > 5000 {
			payloadLength = 5000
		}

		newInfo := AddSessionExecutedIssueTemplate{
			Payload: string(payload[0:payloadLength]),
		}

		if payloadLength > 0 {
			issue = addSessionExecutedIssueTemplate(contentList, newInfo)
		}
	default:
		return fmt.Errorf("unknown actionType: %s", actionType)
	}

	sendJiraIssueUpdate(orgId, session.JiraIssue, issue)
	return nil
}
