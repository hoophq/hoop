package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
)

func CreateIssue(orgId, summary, issueType string, content CreateSessionJiraIssueTemplate) error {
	dbJiraIntegration, err := models.GetJiraIntegration(orgId)
	if err != nil {
		log.Warnf("failed to get Jira integration: %v", err)
		return fmt.Errorf("failed to get Jira integration: %w", err)
	}

	issue := createSessionJiraIssueTemplate(dbJiraIntegration.JiraProjectKey, summary, issueType, content)
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
		return fmt.Errorf("failed to create issue: %s", respBody)
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

	session := pgsession.New()
	if err := session.UpdateJiraIssue(orgId, content.SessionID, issueResponse.Key); err != nil {
		return fmt.Errorf("error updating session with Jira issue: %v", err)
	}

	log.Infof("Session updated with Jira issue key: %s", issueResponse.Key)
	return nil
}

// Function to get the current issue description
func getIssueDescription(orgId, issueKey string) (map[string]interface{}, error) {
	req, err := createJiraRequest(orgId, "GET", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), nil)
	if err != nil {
		log.Warnf("Error creating request to get issue: %v", err)
		return nil, err
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

func updateJiraIssue(orgID, issueKey string, issue map[string]interface{}) error {
	payloadBytes, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to serialize update payload: %v", err)
	}

	req, err := createJiraRequest(orgID, "PUT", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), payloadBytes)
	if err != nil {
		log.Warnf("failed to create request for updating issue: %v", err)
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send update request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update issue description. Status: %s, Body: %s", resp.Status, respBody)
	}

	log.Infof("Issue description updated successfully!")
	return nil
}

func AddReviewCreatedJiraIssue(orgId, issueKey string, newInfo UpdateReviewJiraIssueTemplate) error {
	currentDescription, err := getIssueDescription(orgId, issueKey)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	issue := updateReviewJiraIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewByUserJiraIssue(orgId, issueKey string, newInfo AddNewReviewByUserIssueTemplate) error {
	currentDescription, err := getIssueDescription(orgId, issueKey)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	issue := addNewReviewByUserIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewRejectedJiraIssue(orgId, issueKey string) error {
	currentDescription, err := getIssueDescription(orgId, issueKey)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	issue := addReviewRejectedOrRevokedIssueTemplate(currentDescription["content"].([]interface{}), "rejected")
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewRevokedJiraIssue(orgId, issueKey string) error {
	currentDescription, err := getIssueDescription(orgId, issueKey)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	issue := addReviewRejectedOrRevokedIssueTemplate(currentDescription["content"].([]interface{}), "revoked")
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewReadyJiraIssue(orgId, issueKey string, newInfos AddReviewReadyIssueTemplate) error {
	currentDescription, err := getIssueDescription(orgId, issueKey)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	issue := addReviewReadyIssueTemplate(currentDescription["content"].([]interface{}), newInfos)
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddSessionExecutedJiraIssue(orgId, issueKey string, newInfo AddSessionExecutedIssueTemplate) error {
	currentDescription, err := getIssueDescription(orgId, issueKey)
	if err != nil {
		return fmt.Errorf("failed to fetch current issue description: %v", err)
	}

	issue := addSessionExecutedIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}
