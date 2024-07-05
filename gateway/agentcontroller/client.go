package agentcontroller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/agentcontroller"
)

const defaultApiControllerURL = "https://agentcontroller.hoop.dev"

var errMissingCredentials = errors.New("missing agent controller credentials env")

type apiClient struct {
	credentials string
}

func NewApiClient() (*apiClient, error) {
	credentials := os.Getenv("AGENTCONTROLLER_CREDENTIALS")
	if credentials == "" {
		return nil, errMissingCredentials
	}
	client := &apiClient{credentials: credentials}
	return client, client.Healthz()
}

func (c *apiClient) List() ([]agentcontroller.Deployment, error) {
	var items []agentcontroller.Deployment
	return items, c.http("GET", "/api/agents", nil, &items)
}

func (c *apiClient) Update(req *agentcontroller.AgentRequest) (*agentcontroller.AgentResponse, error) {
	resp := &agentcontroller.AgentResponse{}
	return resp, c.http("PUT", "/api/agents", req, resp)
}

func (c *apiClient) Remove(name, id string) error {
	uri := "/api/agents/" + name + "?id=" + id
	return c.http("DELETE", uri, nil, nil)
}

func (c *apiClient) Healthz() error {
	resp := map[string]any{}
	if err := c.http("GET", "/api/healthz", nil, &resp); err != nil {
		return err
	}
	if resp["status"] != "OK" {
		return fmt.Errorf("api is down")
	}
	return nil
}

func (c *apiClient) http(method, uri string, reqBodyObj, respObj any) error {
	b, err := json.Marshal(reqBodyObj)
	if err != nil {
		return fmt.Errorf("failed encoding request body: %v", err)
	}
	reqBody := bytes.NewBuffer(b)
	apiURI := fmt.Sprintf("%s/%s", defaultApiControllerURL, strings.TrimPrefix(uri, "/"))
	req, err := c.newRequest(method, apiURI, reqBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed obtaining response: %v", err)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		respBodyErr, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api return error, status=%v, body=%v", resp.StatusCode, string(respBodyErr))
	}
	if respObj == nil {
		return nil
	}
	err = json.NewDecoder(resp.Body).Decode(respObj)
	if err != nil {
		return fmt.Errorf("fail decoding response body as json: %v", err)
	}
	return nil
}

func (c *apiClient) newRequest(method, apiURI string, reqBody io.Reader) (*http.Request, error) {
	request, err := http.NewRequest(method, apiURI, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %v", err)
	}
	iat := time.Now().UTC().Unix()
	exp := time.Now().UTC().Add(time.Minute * 5).Unix()
	j := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iat": iat,
		"exp": exp,
	})
	requestToken, err := j.SignedString([]byte(c.credentials))
	if err != nil {
		return nil, fmt.Errorf("failed generating request token, reason=%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %v", requestToken))
	return request, nil
}
