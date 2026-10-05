//go:build integration && parity

package parity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client calls the API of the process under test. Paths are relative to /api.
type Client struct {
	base   string
	header http.Header
}

// Resp is a fully read HTTP response.
type Resp struct {
	Status int
	Header http.Header
	Body   []byte
}

var httpClient = &http.Client{Timeout: 70 * time.Second}

// Do sends body as JSON, or verbatim when it is []byte. A transport error
// stops the check.
func (cl *Client) Do(c *C, method, path string, body any) Resp {
	r, err := cl.do(method, path, body)
	if err != nil {
		c.Fatalf("%s %s: %v", method, path, err)
	}
	return r
}

func (cl *Client) do(method, path string, body any) (Resp, error) {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return Resp{}, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, cl.base+"/api"+path, rd)
	if err != nil {
		return Resp{}, err
	}
	for k, v := range cl.header {
		req.Header[k] = v
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return Resp{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return Resp{}, err
	}
	return Resp{Status: res.StatusCode, Header: res.Header, Body: data}, nil
}

// Expect stops the check unless the status is one of want.
func (r Resp) Expect(c *C, want ...int) Resp {
	for _, w := range want {
		if r.Status == w {
			return r
		}
	}
	c.Fatalf("status %d, want %v: %s", r.Status, want, truncate(r.Body))
	return r
}

// JSON decodes the body into v.
func (r Resp) JSON(c *C, v any) {
	if err := json.Unmarshal(r.Body, v); err != nil {
		c.Fatalf("decoding %s: %v", truncate(r.Body), err)
	}
}

func truncate(b []byte) string {
	const max = 600
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("... (%d bytes)", len(b))
	}
	return string(b)
}
