package pgrest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/runopsio/hoop/common/log"
)

var (
	ErrNotFound      = fmt.Errorf("resource not found")
	ErrUnauthorized  = fmt.Errorf("unauthorized")
	ErrEmptyResponse = fmt.Errorf("empty response")
)

type Client struct {
	apiURL      string
	accessToken string
}

func NewWithContext(org, email, path string) *Client { return newClient(org, email, path) }
func New(path string, a ...any) *Client              { return newClient("", "", fmt.Sprintf(path, a...)) }

func newClient(org, email, path string) *Client {
	j := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"org":   org,
		"email": email,
		"role":  "webuser",
	})
	apiURL := os.Getenv("REST_API")
	if apiURL == "" {
		apiURL = "http://127.0.0.1:8008"
	}
	accessToken, err := j.SignedString([]byte(os.Getenv("JWTKEY")))
	if err != nil {
		panic(err)
	}
	// fmt.Println("ACCESTOKEN", accessToken)
	apiURL = fmt.Sprintf("%s/%s",
		strings.TrimSuffix(os.Getenv("REST_API"), "/"),
		strings.TrimPrefix(path, "/"),
	)
	return &Client{apiURL: apiURL, accessToken: accessToken}
}

func (c *Client) Create(reqBody any) *Response {
	reqHeader := map[string]string{"Accept": "application/vnd.pgrst.object+json"}
	resp := httpRequest(c.apiURL, "POST", c.accessToken, reqHeader, reqBody)
	return &resp
}

func (c *Client) RpcCreate(reqBody any) *Response {
	resp := httpRequest(c.apiURL, "POST", c.accessToken, nil, reqBody)
	return &resp
}

func (c *Client) Upsert(reqBody any) *Response {
	reqHeader := map[string]string{"Prefer": "resolution=merge-duplicates"}
	resp := httpRequest(c.apiURL, "POST", c.accessToken, reqHeader, reqBody)
	return &resp
}

func (c *Client) Patch(reqBody any) *Response {
	reqHeader := map[string]string{"Accept": "application/vnd.pgrst.object+json"}
	resp := httpRequest(c.apiURL, "PATCH", c.accessToken, reqHeader, reqBody)
	return &resp
}

func (c *Client) Update(reqBody any) *Response {
	resp := httpRequest(c.apiURL, "PUT", c.accessToken, nil, reqBody)
	return &resp
}

func (c *Client) FetchOne() *Response {
	reqHeader := map[string]string{"Accept": "application/vnd.pgrst.object+json"}
	resp := httpRequest(c.apiURL, "GET", c.accessToken, reqHeader, nil)
	return &resp
}

func (c *Client) List() *Response {
	resp := httpRequest(c.apiURL, "GET", c.accessToken, nil, nil)
	return &resp
}

// ExactCount returns the total of records in the table, in case of error it returns -1
func (c *Client) ExactCount() int64 {
	reqHeader := map[string]string{"Prefer": "count=exact"}
	resp := httpRequest(c.apiURL, "HEAD", c.accessToken, reqHeader, nil)
	if contentRange := resp.headers.Get("Content-Range"); contentRange != "" {
		_, size, _ := strings.Cut(contentRange, "/")
		if total, err := strconv.ParseInt(size, 10, 64); err == nil {
			return total
		}
	}
	return -1
}

func (c *Client) Delete() *Response {
	resp := httpRequest(c.apiURL, "DELETE", c.accessToken, nil, nil)
	return &resp
}

func httpRequest(apiURL, method, bearerToken string, reqHeaders map[string]string, body any) (resp Response) {
	var req *http.Request

	switch b := body.(type) {
	case []byte:
		req, resp.err = http.NewRequest(method, apiURL, bytes.NewBuffer(b))
	case nil:
		req, resp.err = http.NewRequest(method, apiURL, nil)
	default:
		reqBody, _ := json.Marshal(b)
		if len(reqBody) == 0 {
			resp.err = fmt.Errorf("failed encoding request body")
			return
		}
		log.Infof("request body %v", string(reqBody))
		req, resp.err = http.NewRequest(method, apiURL, bytes.NewBuffer(reqBody))
	}
	if resp.err != nil {
		resp.err = fmt.Errorf("failed creating http request: %v", resp.err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", bearerToken))
	req.Header.Set("Prefer", "return=representation")
	for key, val := range reqHeaders {
		req.Header.Add(key, val)
	}
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		resp.err = fmt.Errorf("failed performing request: %v", err)
		return
	}
	resp.statusCode = httpResp.StatusCode
	resp.headers = httpResp.Header.Clone()
	// resp.contentLength = httpResp.ContentLength

	log.Infof("%v %v %v", resp.statusCode, method, apiURL)
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.err = fmt.Errorf("failed reading http response body, status=%v, content-length=%v, error=%v",
			httpResp.StatusCode, httpResp.ContentLength, err)
		return
	}
	resp.data = respBody
	return
}

type Response struct {
	data       []byte
	statusCode int
	headers    http.Header
	// contentLength int64
	err error
}

// DecodeInto will copy the bytes of the response if obj is []byte
// or unmarshal it to json
func (r *Response) DecodeInto(obj any) error {
	if err := r.Error(); err != nil {
		return err
	}
	// fmt.Println("DATA->>", string(r.data))
	if len(r.data) == 0 || bytes.Equal(r.data, []byte(`[]`)) {
		return ErrNotFound
	}
	switch v := obj.(type) {
	case []byte:
		copy(v, r.data)
	default:
		return json.Unmarshal(r.data, obj)
	}
	return nil
}

func (r *Response) Is2xx() bool {
	return r.statusCode == 200 || r.statusCode == 201 ||
		r.statusCode == 202 || r.statusCode == 204
}

func (r *Response) IsError() bool { return r.err != nil || !r.Is2xx() }
func (r *Response) Error() error {
	// it's ok to coerce to not found
	// when using accept: application/vnd.pgrst.object+json strategy to return objects
	if r.statusCode == http.StatusNotAcceptable {
		return ErrNotFound
	}
	if r.err != nil {
		return r.err
	}
	if !r.Is2xx() {
		return fmt.Errorf("status=%v, response=%v", r.statusCode, string(r.data))
	}
	return nil
}
