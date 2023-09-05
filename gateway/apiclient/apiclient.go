package apiclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
	apitypes "github.com/runopsio/hoop/gateway/apiclient/types"
)

var (
	ErrNotFound    = fmt.Errorf("resource not found")
	hoopVersionStr = version.Get().Version
)

type Client struct {
	apiURL      string
	accessToken string
}

func New(apiURL, accessToken string) *Client {
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
