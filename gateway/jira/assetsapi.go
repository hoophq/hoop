package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hoophq/hoop/gateway/models"
)

const (
	baseAssetsAPI         string = "https://api.atlassian.com/jsm/assets/workspace"
	defaultMaxResults     int    = 50
	maxPaginationRequests int    = 50
)

var defaultRequestTimeout = time.Second * 50

type ErrTimeoutReached struct {
	apiResource string
	query       string
}

func (e ErrTimeoutReached) Error() string {
	return fmt.Sprintf("request timeout (%v) reached fetching resource %q, query=%q",
		defaultRequestTimeout, e.apiResource, e.query)
}

// FetchObjectsByAQL list objects paginated based in the Jira AQL query expression
// https://support.atlassian.com/jira-service-management-cloud/docs/use-assets-query-language-aql/
func FetchObjectsByAQL(config *models.JiraIntegration, limit, offset int, query string) (*AqlResponse, error) {
	if limit > defaultMaxResults {
		limit = defaultMaxResults
	}
	vals := url.Values{}
	vals.Set("maxResults", fmt.Sprintf("%v", limit))
	vals.Set("startAt", fmt.Sprintf("%v", offset))
	vals.Set("includeAttributes", "false")
	return fetchObjectsByAQL(config, vals, query)
}

// https://developer.atlassian.com/cloud/assets/rest/api-group-object/#api-object-aql-post
func fetchObjectsByAQL(config *models.JiraIntegration, queryVals url.Values, query string) (*AqlResponse, error) {
	ctx, cancelFn := context.WithTimeoutCause(context.Background(), defaultRequestTimeout, &ErrTimeoutReached{})
	defer cancelFn()
	workspaceID, err := fetchWorkspaceID(ctx, config)
	if err != nil {
		return nil, err
	}
	totalCount, err := fetchTotalCountObjects(ctx, config, queryVals, workspaceID, query)
	if err != nil {
		return nil, err
	}

	queryPayload := map[string]any{"qlQuery": query}
	requestBody, _ := json.Marshal(queryPayload)
	apiURL := fmt.Sprintf("%s/%s/v1/object/aql?maxResults=%v&includeAttributes=false", baseAssetsAPI, workspaceID, defaultMaxResults)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed creating objects aql query request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	req.URL.RawQuery = queryVals.Encode()
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errCtx, ok := context.Cause(ctx).(*ErrTimeoutReached); ok {
			errCtx.apiResource = "objects-aql"
			errCtx.query = query
			return nil, errCtx
		}
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
	response.TotalCount = totalCount
	return &response, nil
}

func fetchWorkspaceID(ctx context.Context, config *models.JiraIntegration) (string, error) {
	apiURL := fmt.Sprintf("%s/rest/servicedeskapi/assets/workspace", config.URL)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed creating request to obtain workspace id, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(config.User, config.APIToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errCtx, ok := context.Cause(ctx).(*ErrTimeoutReached); ok {
			errCtx.apiResource = "workspace"
			return "", errCtx
		}
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

func fetchTotalCountObjects(ctx context.Context, config *models.JiraIntegration, queryVals url.Values, workspaceID, query string) (int64, error) {
	apiURL := fmt.Sprintf("%s/%s/v1/object/aql/totalcount", baseAssetsAPI, workspaceID)
	queryPayload := map[string]any{"qlQuery": query}
	requestBody, _ := json.Marshal(queryPayload)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return 0, fmt.Errorf("failed creating objects aql query total count request, reason=%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	req.URL.RawQuery = queryVals.Encode()
	req.SetBasicAuth(config.User, config.APIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errCtx, ok := context.Cause(ctx).(*ErrTimeoutReached); ok {
			errCtx.apiResource = "aql-totalcount"
			errCtx.query = query
			return 0, errCtx
		}
		return 0, fmt.Errorf("failed obtaining aql total count, query=%v, reason=%v", query, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("unable to fetch aql query total count, api-url=%v, status=%v, body=%v",
			apiURL, resp.StatusCode, string(body))
	}
	var response map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return 0, fmt.Errorf("failed decoding objects aql query total count response, reason=%v", err)
	}
	totalCount, ok := response["totalCount"].(float64)
	if !ok {
		return 0, fmt.Errorf("unable to decode totalCount attribute from aql query, type=%T, value=%#q",
			response["totalCount"], response["totalCount"])
	}
	return int64(totalCount), nil
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
