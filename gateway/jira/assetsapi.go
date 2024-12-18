package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/hoophq/hoop/gateway/models"
)

const (
	baseAssetsAPI         string = "https://api.atlassian.com/jsm/assets/workspace"
	defaultMaxResults     int    = 10
	maxPaginationRequests int    = 50
)

// FetchObjectsByAQL list objects paginated based in the Jira AQL query expression
// https://support.atlassian.com/jira-service-management-cloud/docs/use-assets-query-language-aql/
func FetchObjectsByAQL(config *models.JiraIntegration, query string, a ...any) (items []AqlResponse, err error) {
	vals := url.Values{}
	vals.Set("maxResults", fmt.Sprintf("%v", defaultMaxResults))
	for i := 0; ; i++ {
		vals.Set("startAt", fmt.Sprintf("%v", defaultMaxResults*i))
		resp, err := fetchObjectsByAQL(config, vals, query, a...)
		if err != nil {
			return nil, err
		}
		items = append(items, *resp)
		if resp.Last {
			break
		}
		if i >= maxPaginationRequests {
			return nil, fmt.Errorf("reached max (%v) pagination requests", maxPaginationRequests)
		}

	}
	return
}

// https://developer.atlassian.com/cloud/assets/rest/api-group-object/#api-object-aql-post
func fetchObjectsByAQL(config *models.JiraIntegration, queryVals url.Values, query string, a ...any) (*AqlResponse, error) {
	query = fmt.Sprintf(query, a...)
	workspaceID, err := fetchWorkspaceID(config)
	if err != nil {
		return nil, err
	}
	queryPayload := map[string]any{"qlQuery": query}
	requestBody, _ := json.Marshal(queryPayload)
	apiURL := fmt.Sprintf("%s/%s/v1/object/aql?maxResults=%v", baseAssetsAPI, workspaceID, defaultMaxResults)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed creating objects aql query request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	req.URL.RawQuery = queryVals.Encode()
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed fetching asset objects, query=%v, reason=%v", query, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unable to fetch asset objects, api-url=%v, status=%v, body=%v",
			apiURL, resp.StatusCode, string(body))
	}
	var response AqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed decoding objects aql query response, reason=%v", err)
	}
	return &response, nil
}

func fetchWorkspaceID(config *models.JiraIntegration) (string, error) {
	apiURL := fmt.Sprintf("%s/rest/servicedeskapi/assets/workspace", config.URL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed creating request to obtain workspace id, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed perform request to obtain workspace id, reason=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unable to obtain workspace id, api-url=%v, status=%v, body=%v",
			apiURL, resp.StatusCode, string(body))
	}
	return decodeWorkspaceID(resp.Body)
}

func decodeWorkspaceID(body io.Reader) (string, error) {
	var res map[string]any
	if err := json.NewDecoder(body).Decode(&res); err != nil {
		return "", fmt.Errorf("failed decoding workspace id response, reason=%v", err)
	}
	values, ok := res["values"].([]any)
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("failed decoding values attribute, type=%T, empty=%v",
			values, len(values) == 0)
	}
	object, ok := values[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("failed decoding to map, type=%T", values[0])
	}
	return fmt.Sprintf("%v", object["workspaceId"]), nil
}
