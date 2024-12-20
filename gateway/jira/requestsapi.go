package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
)

func CreateCustomerRequest(tmpl *models.JiraIssueTemplate, config *models.JiraIntegration, fields CustomFields) (*RequestResponse, error) {
	serviceDeskID, err := fetchServiceDeskID(config, tmpl.ProjectKey)
	if err != nil {
		return nil, err
	}

	if _, hasSummary := fields["summary"]; !hasSummary {
		fields["summary"] = "Hoop Session"
	}
	issue := IssueFieldsV2[CustomFields]{
		ServiceDeskID:    serviceDeskID,
		RequestTypeID:    tmpl.IssueTypeName,
		IsAdfRequest:     false,
		IssueFieldValues: IssueFieldValues[CustomFields]{fields},
	}
	issuePayload, err := json.Marshal(issue)
	if err != nil {
		return nil, fmt.Errorf("failed encoding issue payload, reason=%v", err)
	}
	log.Infof("creating jira issue request with payload: %v", string(issuePayload))
	apiURL := fmt.Sprintf("%s/rest/servicedeskapi/request", config.URL)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(issuePayload))
	if err != nil {
		return nil, fmt.Errorf("failed creating request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed creating jira issue, reason=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unable to create jira issue, status=%v, body=%v",
			resp.StatusCode, string(body))
	}
	var response RequestResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed decoding jira issue response, reason=%v", err)
	}
	return &response, nil
}

func fetchServiceDeskID(config *models.JiraIntegration, projectKey string) (string, error) {
	// temporary to avoid having to paginate
	if val := os.Getenv("JIRA_SERVICE_DESK_ID"); val != "" {
		return val, nil
	}
	apiURL := fmt.Sprintf("%s/rest/servicedeskapi/servicedesk?limit=100", config.URL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed creating service desk http request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed performing service desk http request for %s, reason=%v", projectKey, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unable to list service desk resources, api-url=%v, status=%v, body=%v",
			apiURL, resp.StatusCode, string(body))
	}
	var obj ServiceDesk
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return "", fmt.Errorf("failed decoding service desk payload, reason=%v", err)
	}
	for _, val := range obj.Values {
		if val.ProjectKey == projectKey {
			return val.ID, nil
		}
	}
	if !obj.IsLastPage {
		return "", fmt.Errorf("unable to find service desk id for %v, pagination not implemented", projectKey)
	}
	log.Warnf("unable to find project key %v, values=%v", projectKey, obj.Values)
	return "", fmt.Errorf("unable to find project key %v", projectKey)
}
