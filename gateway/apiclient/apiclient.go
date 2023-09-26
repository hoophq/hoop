package apiclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

type AuthError struct {
	err    error
	reason string
}

type Client struct {
	apiURL      string
	accessToken string
}

func New(apiURL, accessToken string) *Client {
	// TODO: add timeout to requests
	return &Client{apiURL: apiURL, accessToken: accessToken}
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
	req.Header.Set("User-Agent", fmt.Sprintf("apiclient/%s", hoopVersionStr))
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
	req.Header.Set("x-backend-api", "express")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", bearerToken))
	req.Header.Set("User-Agent", fmt.Sprintf("apiclient/%s", hoopVersionStr))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed performing request: %v", err)
	}
	log.Debugf("http response %v, content-length=%v", resp.StatusCode, resp.ContentLength)
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
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

// TODO: implement it
func (c *Client) CloseSession(data map[string]any) error {
	log.Infof("[implement me] close session with success on api!")
	return nil
}

// TODO: implement it
func (c *Client) OpenSession() (string, error) {
	log.Infof("[implement me] opened session with success on api")
	return uuid.NewString(), nil
}
