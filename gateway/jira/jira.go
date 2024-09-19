package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"libhoop/log"
	"net/http"

	"github.com/hoophq/hoop/gateway/pgrest"
	jiraintegration "github.com/hoophq/hoop/gateway/pgrest/jiraintegration"
	pgsession "github.com/hoophq/hoop/gateway/pgrest/session"
)

// Function to build the issue structure
func BuildIssue(projectKey, summary, issueType, description string) Issue {
	return Issue{
		Fields: IssueFields{
			Project:   Project{Key: projectKey},
			Summary:   summary,
			Issuetype: Issuetype{Name: issueType},
			Description: map[string]interface{}{
				"type":    "doc",
				"version": 1,
				"content": []interface{}{
					map[string]interface{}{
						"type": "paragraph",
						"content": []interface{}{
							map[string]interface{}{
								"type": "text",
								"text": description,
							},
						},
					},
				},
			},
		},
	}
}

// Simplified function to create an issue in JIRA
func CreateIssueSimple(ctx pgrest.OrgContext, summary, issueType, description, sessionID string) error {
	jiraIntegrations := jiraintegration.NewJiraIntegrations()
	integration, err := jiraIntegrations.GetJiraIntegration(ctx)

	if err != nil {
		log.Errorf("Failed to get Jira integration: %v", err)
		return fmt.Errorf("failed to get Jira integration: %w", err)
	}
	if integration == nil {
		log.Errorf("No Jira integration found for org_id: %s", ctx.GetOrgID())
		return fmt.Errorf("no Jira integration found")
	}

	if integration.Status != jiraintegration.JiraIntegrationStatusActive {
		log.Errorf("Jira integration is not active for org_id: %s", ctx.GetOrgID())
		return fmt.Errorf("jira integration is not enabled")
	}

	// Build the issue structure using BuildIssue
	issue := BuildIssue(integration.JiraProjectKey, summary, issueType, description)

	// Serialize the issue to JSON
	body, err := json.Marshal(issue)
	if err != nil {
		log.Errorf("Error serializing issue: %v", err)
		return err
	}

	// Create the request to JIRA
	req, err := createJiraRequest(ctx, "POST", "/rest/api/3/issue", body)
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

	s := pgsession.New()
	if err := s.UpdateJiraIssue(ctx, sessionID, issueResponse.Key); err != nil {
		log.Errorf("Error updating session with Jira issue: %v", err)
		return err
	}

	log.Infof("Session updated with Jira issue key: %s", issueResponse.Key)
	return nil
}

// Function to get the current issue description
func GetIssueDescription(ctx pgrest.OrgContext, issueKey string) (map[string]interface{}, error) {
	// Create a request to get the issue from JIRA
	req, err := createJiraRequest(ctx, "GET", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), nil)
	if err != nil {
		log.Errorf("Error creating request to get issue: %v", err)
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

// UpdateJiraIssueDescription updates the description of an existing JIRA issue.
func UpdateJiraIssueDescription(ctx pgrest.OrgContext, issueKey, newInfo string) error {
	// Step 1: Get the current issue description
	currentDescription, err := GetIssueDescription(ctx, issueKey)
	if err != nil {
		log.Errorf("Failed to fetch current issue description: %v", err)
		return err
	}

	// Append new content below a divider
	newContent := []interface{}{
		map[string]interface{}{
			"type": "rule", // Divider
		},
		map[string]interface{}{
			"type": "paragraph",
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": newInfo, // New text to append
				},
			},
		},
	}

	// Append the new content to the existing description
	content := currentDescription["content"].([]interface{})
	content = append(content, newContent...)

	// Step 3: Build the update payload
	payload := map[string]interface{}{
		"update": map[string]interface{}{
			"description": []interface{}{
				map[string]interface{}{
					"set": map[string]interface{}{
						"type":    "doc",
						"version": 1,
						"content": content, // Description with append
					},
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("Failed to serialize update payload: %v", err)
		return err
	}

	req, err := createJiraRequest(ctx, "PUT", fmt.Sprintf("/rest/api/3/issue/%s", issueKey), payloadBytes)
	if err != nil {
		log.Errorf("Failed to create request for updating issue: %v", err)
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
