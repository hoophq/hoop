package jira

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

func hasJiraIntegrationEnabled(orgID string) error {
	dbJiraIntegration, err := models.GetJiraIntegration(orgID)

	if err != nil {
		return fmt.Errorf("failed to get Jira integration: %v", err)
	}
	if dbJiraIntegration == nil {
		return fmt.Errorf("no Jira integration found for org_id: %s", orgID)
	}

	if dbJiraIntegration.Status != models.JiraIntegrationStatusActive {
		return fmt.Errorf("jira integration is not active for org_id: %s", orgID)
	}

	return nil
}

func createJiraRequest(orgId, method, endpoint string, body []byte) (*http.Request, error) {
	dbJiraIntegration, err := models.GetJiraIntegration(orgId)

	if err != nil {
		return nil, fmt.Errorf("failed to get Jira integration: %s", err)
	}
	if dbJiraIntegration == nil {
		return nil, fmt.Errorf("no Jira integration found for org_id: %s", orgId)
	}

	baseURL := dbJiraIntegration.URL
	apiToken := dbJiraIntegration.APIToken
	userEmail := dbJiraIntegration.User

	url := baseURL + endpoint

	authString := userEmail + ":" + apiToken
	encodedAuth := base64.StdEncoding.EncodeToString([]byte(authString))

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Authorization", "Basic "+encodedAuth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	log.Infof("Request %s created for %s", method, url)
	return req, nil
}
