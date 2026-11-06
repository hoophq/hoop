package mcpgateway_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/mcpgateway/internal/testupstream"
)

var piiValues = map[string]string{"Jane Doe": "PERSON", "jane.doe@example.com": "EMAIL_ADDRESS", "123-45-6789": "US_SSN"}

// stubMask masks each PII value as <ENTITY> and fails on "boom".
func stubMask(text string) (string, []string, int, error) {
	if strings.Contains(text, "boom") {
		return "", nil, 0, errors.New("stub masker failed")
	}
	n := 0
	var entities []string
	for value, entity := range piiValues {
		if c := strings.Count(text, value); c > 0 {
			text = strings.ReplaceAll(text, value, "<"+entity+">")
			n += c
			entities = append(entities, entity)
		}
	}
	return text, entities, n, nil
}

func hasPII(s string) bool {
	for value := range piiValues {
		if strings.Contains(s, value) {
			return true
		}
	}
	return false
}

func startMaskLane(t *testing.T, ln *testLane, maskResults bool) *client {
	t.Helper()
	up := httptest.NewServer(testupstream.Handler(testupstream.NewServer("")))
	t.Cleanup(up.Close)
	_, url := serveLane(t, daemon.MCPGatewayLane{Name: "u", Listen: "127.0.0.1:0", Upstream: up.URL + "/mcp",
		Transport: daemon.MCPTransportStreamableHTTP, MaskResults: maskResults, Open: ln.open})
	c := &client{url: url}
	c.init(t)
	return c
}

type rpcReply struct {
	Result struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Resource struct {
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeReply(t *testing.T, body string) rpcReply {
	t.Helper()
	var r rpcReply
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("reply %s: %v", body, err)
	}
	return r
}

const maskedCustomer = "<PERSON>, email <EMAIL_ADDRESS>, SSN <US_SSN>"

// tools/call content[].text comes back masked, and the session records the
// count of mcpproxy's redacted event: three values in the text and three in
// structuredContent.
func TestAToolResultTextIsMasked(t *testing.T) {
	ln := &testLane{mask: stubMask}
	c := startMaskLane(t, ln, true)
	r := decodeReply(t, c.call(t, 3, "customer_lookup", `{"id":"42"}`))
	if r.Error != nil || len(r.Result.Content) != 1 || r.Result.Content[0].Text != "Customer 42: "+maskedCustomer {
		t.Fatalf("content: %+v", r)
	}
	if got := ln.session(0).maskedRecords(); !slices.Equal(got, []string{"EMAIL_ADDRESS,PERSON,US_SSN:6"}) {
		t.Fatalf("masked records %v, want one with 6 values", got)
	}
}

// resources/read contents[].text comes back masked.
func TestAResourceReadIsMasked(t *testing.T) {
	ln := &testLane{mask: stubMask}
	c := startMaskLane(t, ln, true)
	_, body := c.post(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":%q}}`, testupstream.CustomerURI))
	r := decodeReply(t, body)
	if r.Error != nil || len(r.Result.Contents) != 1 || r.Result.Contents[0].Text != maskedCustomer {
		t.Fatalf("resources/read: %s", body)
	}
}

// The resource.text of an embedded resource in content[] comes back masked.
func TestANestedResourceTextIsMasked(t *testing.T) {
	ln := &testLane{mask: stubMask}
	c := startMaskLane(t, ln, true)
	body := c.call(t, 3, "customer_record", `{"id":"42"}`)
	r := decodeReply(t, body)
	if r.Error != nil || len(r.Result.Content) != 1 || r.Result.Content[0].Type != "resource" || r.Result.Content[0].Resource.Text != maskedCustomer {
		t.Fatalf("embedded resource: %s", body)
	}
}

// Every string of structuredContent comes back masked, at any depth; keys and
// numbers are kept.
func TestEveryStructuredContentStringIsMasked(t *testing.T) {
	ln := &testLane{mask: stubMask}
	c := startMaskLane(t, ln, true)
	body := c.call(t, 3, "customer_profile", `{"id":"42"}`)
	r := decodeReply(t, body)
	var got, want any
	if err := json.Unmarshal(r.Result.StructuredContent, &got); err != nil {
		t.Fatalf("structuredContent: %s", body)
	}
	_ = json.Unmarshal([]byte(`{
		"customer": {"name": "<PERSON>", "email": "<EMAIL_ADDRESS>", "ssn": "<US_SSN>"},
		"contacts": [{"kind": "email", "value": "<EMAIL_ADDRESS>"}, {"kind": "backup", "value": "<PERSON> <<EMAIL_ADDRESS>>"}],
		"notes": ["verified SSN <US_SSN>"],
		"visits": 3
	}`), &want)
	if !reflect.DeepEqual(got, want) || hasPII(body) {
		t.Fatalf("structuredContent: %s", body)
	}
}

// A masker error withholds that result with a JSON-RPC error that quotes
// nothing of it. The MCP session and its sidecar session stay open.
func TestAMaskerErrorWithholdsTheResultAndTheSessionSurvives(t *testing.T) {
	ln := &testLane{mask: stubMask}
	c := startMaskLane(t, ln, true)
	body := c.call(t, 3, "echo", `{"message":"boom"}`)
	r := decodeReply(t, body)
	if r.Error == nil || r.Error.Code != -32010 || !strings.Contains(r.Error.Message, "result withheld") || strings.Contains(body, "boom") {
		t.Fatalf("a result the masker failed on: %s", body)
	}
	if body := c.call(t, 4, "echo", `{"message":"hello"}`); !strings.Contains(body, `"hello"`) {
		t.Fatalf("the next call on the session: %s", body)
	}
	_, acts, closed := ln.session(0).snapshot()
	if ln.count() != 1 || closed || !slices.Contains(acts, "mcp.tool_denied") || !strings.Contains(ln.session(0).everything(), "rule=redact") {
		t.Fatalf("sessions %d, closed %v, acts %v", ln.count(), closed, acts)
	}
	if got := ln.session(0).maskedRecords(); len(got) != 0 {
		t.Fatalf("masked records %v, want none", got)
	}
}

// A masked result the session cannot record is withheld, as on a grpc lane.
func TestAMaskedResultThatIsNotRecordedIsWithheld(t *testing.T) {
	ln := &testLane{mask: stubMask, recordErr: errors.New("audit sink down")}
	c := startMaskLane(t, ln, true)
	body := c.call(t, 3, "customer_lookup", `{"id":"42"}`)
	r := decodeReply(t, body)
	if r.Error == nil || r.Error.Code != -32010 || !strings.Contains(r.Error.Message, "audit trail unavailable") || hasPII(body) {
		t.Fatalf("an unrecorded masked result: %s", body)
	}
	if body := c.call(t, 4, "echo", `{"message":"hello"}`); !strings.Contains(body, `"hello"`) {
		t.Fatalf("the next call on the session: %s", body)
	}
}

// A lane without mask rules forwards results as the upstream sent them.
func TestALaneWithoutMaskRulesForwardsResultsAsSent(t *testing.T) {
	ln := &testLane{mask: stubMask}
	c := startMaskLane(t, ln, false)
	if body := c.call(t, 3, "customer_lookup", `{"id":"42"}`); !strings.Contains(body, "jane.doe@example.com") {
		t.Fatalf("result: %s", body)
	}
}
