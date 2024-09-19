package jira

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"libhoop/log"
	"net/http"

	"github.com/hoophq/hoop/gateway/pgrest"
	jiraintegration "github.com/hoophq/hoop/gateway/pgrest/jiraintegration"
)

// Function to build the request to JIRA
func createJiraRequest(ctx pgrest.OrgContext, method, endpoint string, body []byte) (*http.Request, error) {

	jiraIntegrations := jiraintegration.NewJiraIntegrations()
	integration, err := jiraIntegrations.GetJiraIntegration(ctx)

	if err != nil {
		log.Errorf("Failed to get Jira integration: %v", err)
		return nil, fmt.Errorf("failed to get Jira integration: %w", err)
	}
	if integration == nil {
		log.Errorf("No Jira integration found for org_id: %s", ctx.GetOrgID())
		return nil, fmt.Errorf("no Jira integration found")
	}

	if integration.Status != jiraintegration.JiraIntegrationStatusActive {
		log.Errorf("Jira integration is not active for org_id: %s", ctx.GetOrgID())
		return nil, fmt.Errorf("jira integration is not enabled")
	}

	baseURL := integration.JiraURL
	apiToken := integration.JiraAPIToken
	userEmail := integration.JiraUser

	if userEmail == "" {
		log.Errorf("The variable JIRA_USER_EMAIL is not set")
		return nil, fmt.Errorf("user email not found")
	}

	if baseURL == "" {
		log.Errorf("The variable JIRA_BASE_URL is not set")
		return nil, fmt.Errorf("base URL not found")
	}

	if apiToken == "" {
		log.Errorf("The variable JIRA_API_TOKEN is not set")
		return nil, fmt.Errorf("token not found")
	}

	// Create the full URL
	url := baseURL + endpoint

	// Create encoded auth string
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
