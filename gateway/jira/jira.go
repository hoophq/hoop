package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"libhoop/log"
	"net/http"

	"github.com/hoophq/hoop/gateway/models"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
)

func CreateIssue(orgId, summary, issueType string, content CreateSessionJiraIssueTemplate) error {
	dbJiraIntegration, err := models.GetJiraIntegration(orgId)
	if err != nil {
		log.Warnf("Failed to get Jira integration: %v", err)
		return fmt.Errorf("failed to get Jira integration: %w", err)
	}

	issue := createSessionJiraIssueTemplate(dbJiraIntegration.JiraProjectKey, summary, issueType, content)
	body, err := json.Marshal(issue)
	if err != nil {
		log.Errorf("Error serializing issue: %v", err)
		return err
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
		log.Errorf("Error sending request: %v", err)
		return err
	}
	defer resp.Body.Close()

	// Read the response
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		log.Errorf("Failed to create issue: %s", respBody)
		return err
	}

	log.Infof("Issue created successfully!")
	log.Infof("Updating session")

	// Parse the response to get the issue key
	var issueResponse struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(respBody, &issueResponse); err != nil {
		log.Errorf("Error parsing issue response: %v", err)
		return err
	}

	session := pgsession.New()
	if err := session.UpdateJiraIssue(orgId, content.SessionID, issueResponse.Key); err != nil {
		log.Errorf("Error updating session with Jira issue: %v", err)
		return err
	}

	log.Infof("Session updated with Jira issue key: %s", issueResponse.Key)
	return nil
}

// Function to get the current issue description
func GetIssueDescription(orgId, issueKey string) (map[string]interface{}, error) {
	req, err := createJiraRequest(orgId, "GET", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), nil)
	if err != nil {
		log.Warnf("Error creating request to get issue: %v", err)
		return nil, err
	}

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Errorf("Error sending request: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	// Check if the response was successful
	if resp.StatusCode != http.StatusOK {
		log.Errorf("Error fetching issue description. Status: %d", resp.StatusCode)
		return nil, fmt.Errorf("failed to fetch issue description, status: %d", resp.StatusCode)
	}

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorf("Error reading response: %v", err)
		return nil, err
	}

	// Parse the JSON response to get the description
	var issueData map[string]interface{}
	if err := json.Unmarshal(body, &issueData); err != nil {
		log.Errorf("Error parsing JSON: %v", err)
		return nil, err
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
		log.Errorf("Failed to serialize update payload: %v", err)
		return err
	}

	req, err := createJiraRequest(orgID, "PUT", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), payloadBytes)
	if err != nil {
		log.Warnf("Failed to create request for updating issue: %v", err)
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Errorf("Failed to send update request: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		log.Errorf("Failed to update issue description. Status: %s, Body: %s", resp.Status, respBody)
		return fmt.Errorf("failed to update issue description, status: %s", resp.Status)
	}

	log.Infof("Issue description updated successfully!")
	return nil
}

func AddReviewCreatedJiraIssue(orgId, issueKey string, newInfo UpdateReviewJiraIssueTemplate) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := updateReviewJiraIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewByUserJiraIssue(orgId, issueKey string, newInfo AddNewReviewByUserIssueTemplate) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := addNewReviewByUserIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewRejectedJiraIssue(orgId, issueKey string) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := addReviewRejectedOrRevokedIssueTemplate(currentDescription["content"].([]interface{}), "rejected")
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewRevokedJiraIssue(orgId, issueKey string) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := addReviewRejectedOrRevokedIssueTemplate(currentDescription["content"].([]interface{}), "revoked")
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddReviewReadyJiraIssue(orgId, issueKey string, newInfos AddReviewReadyIssueTemplate) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := addReviewReadyIssueTemplate(currentDescription["content"].([]interface{}), newInfos)
	return updateJiraIssue(orgId, issueKey, issue)
}

func AddSessionExecutedJiraIssue(orgId, issueKey string, newInfo AddSessionExecutedIssueTemplate) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := addSessionExecutedIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}

func UpdateJiraIssueDescription(orgId, issueKey string, newInfo UpdateReviewJiraIssueTemplate) error {
	currentDescription, err := GetIssueDescription(orgId, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	issue := updateReviewJiraIssueTemplate(currentDescription["content"].([]interface{}), newInfo)
	return updateJiraIssue(orgId, issueKey, issue)
}
