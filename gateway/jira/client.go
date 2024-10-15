package jira

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

func createJiraRequest(orgId, method, endpoint string, body []byte) (*http.Request, error) {
	dbJiraIntegration, err := models.GetJiraIntegration(orgId)

	if err != nil {
		log.Warnf("Failed to get Jira integration: %v", err)
		return nil, fmt.Errorf("failed to get Jira integration: %w", err)
	}
	if dbJiraIntegration == nil {
		log.Warnf("No Jira integration found for org_id: %s", orgId)
		return nil, fmt.Errorf("no Jira integration found")
	}

	if dbJiraIntegration.Status != models.JiraIntegrationStatusActive {
		log.Warnf("Jira integration is not active for org_id: %s", orgId)
		return nil, fmt.Errorf("jira integration is not enabled")
	}

	baseURL := dbJiraIntegration.JiraURL
	apiToken := dbJiraIntegration.JiraAPIToken
	userEmail := dbJiraIntegration.JiraUser

	if userEmail == "" {
		log.Warnf("The variable JIRA_USER_EMAIL is not set")
		return nil, fmt.Errorf("user email not found")
	}

	if baseURL == "" {
		log.Warnf("The variable JIRA_BASE_URL is not set")
		return nil, fmt.Errorf("base URL not found")
	}

	if apiToken == "" {
		log.Warnf("The variable JIRA_API_TOKEN is not set")
		return nil, fmt.Errorf("token not found")
	}

	url := baseURL + endpoint

	authString := userEmail + ":" + apiToken
	encodedAuth := base64.StdEncoding.EncodeToString([]byte(authString))

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		log.Errorf("Error creating request: %v", err)
		return nil, err
	}

	req.Header.Set("Authorization", "Basic "+encodedAuth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	log.Infof("Request %s created for %s", method, url)
	return req, nil
}
