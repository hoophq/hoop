package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
)

var (
	ErrNotFound     = fmt.Errorf("resource not found")
	ErrUnauthorized = fmt.Errorf("unauthorized")
	hoopVersionStr  = version.Get().Version
)

type Client struct {
	apiURL      string
	accessToken string
}

func New(accessToken string) *Client {
	nodeApiUrl := os.Getenv("NODE_API_URL")
	if _, err := url.Parse(nodeApiUrl); err != nil {
		nodeApiUrl = "http://127.0.0.1:4001"
	}
	// TODO: add timeout to requests
	return &Client{apiURL: nodeApiUrl, accessToken: accessToken}
}

func httpGetRequest(apiURL, accessToken string, into any) error {
	log.Debugf("performing http request at GET %v", apiURL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed creating http request, err=%v", err)
	}
	req.Header.Set("x-backend-api", "express")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", accessToken))
	req.Header.Set("User-Agent", fmt.Sprintf("hoopgateway/%s", hoopVersionStr))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	log.Infof("http response %v, content-length=%v", resp.StatusCode, resp.ContentLength)
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(resp.Body).Decode(into)
	case http.StatusNotFound:
		return ErrNotFound
	}
	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("failed performing request, status=%v, body=%v",
		resp.StatusCode, string(respBody))
}

func httpRequest(apiURL, method, bearerToken string, body, into any) (err error) {
	log.Debugf("performing http request at %s %s", method, apiURL)
	var req *http.Request

	switch b := body.(type) {
	case []byte:
		req, err = http.NewRequest(method, apiURL, bytes.NewBuffer(b))
	case nil:
		req, err = http.NewRequest(method, apiURL, nil)
	default:
		reqBody, _ := json.Marshal(b)
		if len(reqBody) == 0 {
			return fmt.Errorf("failed encoding request body")
		}
		req, err = http.NewRequest(method, apiURL, bytes.NewBuffer(reqBody))
	}
	if err != nil {
		return fmt.Errorf("failed creating http request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", bearerToken))
	// when bearer token is a session token, add this additional header
	if _, err := uuid.Parse(bearerToken); err == nil {
		req.Header.Set("post-save-session-token", bearerToken)
	}
	req.Header.Set("User-Agent", fmt.Sprintf("hoopgateway/%s", hoopVersionStr))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed performing request: %v", err)
	}
	log.Debugf("http response %v, content-length=%v", resp.StatusCode, resp.ContentLength)
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusCreated:
		if resp.ContentLength > 0 || resp.ContentLength == -1 {
			if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
				return fmt.Errorf("failed decoding response body, status=%v, content-length=%v, error=%v",
					resp.StatusCode, resp.ContentLength, err)
			}
		}
		return nil
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized:
		if resp.ContentLength > 0 || resp.ContentLength == -1 {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("unauthorized, reason=%v", string(respBody))
		}
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	}
	var respBody []byte
	if resp.ContentLength > 0 || resp.ContentLength == -1 {
		respBody, _ = io.ReadAll(resp.Body)
	}
	return fmt.Errorf("failed performing request, content-length=%v, status=%v, body=%v",
		resp.ContentLength, resp.StatusCode, string(respBody))
}

func (c *Client) GetConnection(name string) (*apitypes.Connection, error) {
	var conn apitypes.Connection
	err := httpGetRequest(fmt.Sprintf("%s/api/connections/%s", c.apiURL, name), c.accessToken, &conn)
	switch err {
	case ErrNotFound:
		return nil, err
	case nil: // noop
	default:
		log.Warnf("failed obtaining connection %v, reason=%v", name, err)
		return nil, err
	}
	if conn.Secrets == nil {
		conn.Secrets = map[string]any{}
	}
	return &conn, nil
}

func (c *Client) AuthAgent(req apitypes.AgentAuthRequest) (*apitypes.Agent, error) {
	var resp apitypes.Agent
	err := httpRequest(
		fmt.Sprintf("%s/api/auth/agents", c.apiURL),
		"POST",
		c.accessToken,
		&req,
		&resp,
	)
	if err != nil {
		return nil, err
	}
	if resp.ID == "" || resp.Name == "" || resp.OrgID == "" {
		return nil, fmt.Errorf("response body is missing required attributes")
	}
	return &resp, nil
}

// AuthClientKeys will be deprecated once the customer groove stop using it
func (c *Client) AuthClientKeys() (*apitypes.Agent, error) {
	var resp apitypes.Agent
	err := httpRequest(
		fmt.Sprintf("%s/api/auth/clientkeys", c.apiURL),
		"POST",
		c.accessToken,
		nil,
		&resp,
	)
	if err != nil {
		return nil, err
	}
	if resp.ID == "" || resp.Name == "" || resp.OrgID == "" {
		return nil, fmt.Errorf("response body is missing required attributes")
	}
	return &resp, nil
}

func (c *Client) CloseSession(sid, sessionToken string, req apitypes.CloseSessionRequest) error {
	resp := map[string]any{}
	return httpRequest(
		fmt.Sprintf("%s/api/sessions/%s", c.apiURL, sid),
		"PUT",
		sessionToken,
		&req,
		&resp,
	)
}

func (c *Client) OpenSession(sid, connectionName, verb, input string) (*apitypes.OpenSessionResponse, error) {
	urlPath := fmt.Sprintf("%s/api/sessions", c.apiURL)
	var resp apitypes.OpenSessionResponse
	reqBody := map[string]any{
		"id":         sid,
		"connection": connectionName,
		"verb":       verb,
	}
	if input != "" {
		reqBody["script"] = input
	}
	return &resp, httpRequest(urlPath, "POST", c.accessToken, reqBody, &resp)
}
