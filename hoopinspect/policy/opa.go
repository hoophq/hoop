package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hoophq/hoopinspect"
)

// OPAClient evaluates statements against an Open Policy Agent Data API
// endpoint.
//
// # Why this shape
//
// InfoSec teams that already run OPA will not adopt a second policy system.
// The point of this client is that hoopinspect does NOT own policy — it owns
// the input document. Envoy's postgres_proxy can hand OPA a resource name and
// an operation verb; this hands it the statement text, the normalized
// operation, the table list and the protocol, in one shape for four
// protocols.
//
// The request body is:
//
//	{"input": { ...Statement... , "user": "...", "connection": "..."}}
//
// and the expected response is OPA's standard:
//
//	{"result": {"allow": true}}
//	{"result": {"allow": false, "message": "..."}}
//
// A bare boolean result is also accepted, so `data.hoop.allow` written as a
// simple rule works without a wrapper object.
type OPAClient struct {
	// URL is the full decision endpoint, e.g.
	// http://opa:8181/v1/data/hoop/inspect
	URL string

	// HTTPClient is used for requests. When nil, a client with Timeout is
	// created on first use.
	HTTPClient *http.Client

	// Timeout bounds a single decision. OPA sits on the data path, so this
	// must stay small; a slow policy engine should fail the request, not hang
	// the connection. Defaults to 2s.
	Timeout time.Duration

	// FailOpen allows the statement when OPA cannot be reached or returns a
	// malformed response. Default false (deny).
	FailOpen bool

	// Context carries extra fields into input alongside the statement: the
	// authenticated user, the hoop connection name, a correlation id.
	// Evaluated once per client, so put per-connection facts here and
	// per-statement facts in the Statement itself.
	Context map[string]string
}

// opaRequest is the wire body sent to OPA.
type opaRequest struct {
	Input opaInput `json:"input"`
}

// opaInput is the document a Rego policy sees as `input`.
//
// Field names are snake_case and stable: they are a public contract with
// whoever writes the Rego, and renaming one silently breaks their policy.
type opaInput struct {
	Protocol  string                  `json:"protocol"`
	Direction string                  `json:"direction"`
	Statement string                  `json:"statement"`
	Operation string                  `json:"operation"`
	Tables    []string                `json:"tables,omitempty"`
	Database  string                  `json:"database,omitempty"`
	HTTP      *hoopinspect.HTTPDetail `json:"http,omitempty"`
	Metadata  map[string]string       `json:"metadata,omitempty"`
	Context   map[string]string       `json:"context,omitempty"`
}

// opaResponse models OPA's Data API reply. Result is deliberately typed as
// json.RawMessage so both an object and a bare boolean decode.
type opaResponse struct {
	Result json.RawMessage `json:"result"`
}

type opaResultObject struct {
	Allow   *bool  `json:"allow"`
	Denied  *bool  `json:"denied"`
	Message string `json:"message"`
	Rule    string `json:"rule"`
}

// Evaluate implements Evaluator.
//
// It uses context.Background with the client's Timeout. Use EvaluateContext to
// propagate a caller's cancellation.
func (c *OPAClient) Evaluate(stmt hoopinspect.Statement) Verdict {
	return c.EvaluateContext(context.Background(), stmt)
}

// EvaluateContext evaluates under a caller-supplied context.
func (c *OPAClient) EvaluateContext(ctx context.Context, stmt hoopinspect.Statement) Verdict {
	body, err := json.Marshal(opaRequest{Input: opaInput{
		Protocol:  string(stmt.Protocol),
		Direction: string(stmt.Direction),
		Statement: stmt.Text,
		Operation: string(stmt.Operation),
		Tables:    stmt.Tables,
		Database:  stmt.Database,
		HTTP:      stmt.HTTP,
		Metadata:  stmt.Metadata,
		Context:   c.Context,
	}})
	if err != nil {
		return c.failure(fmt.Errorf("policy/opa: encoding input: %w", err))
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return c.failure(fmt.Errorf("policy/opa: building request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	resp, err := client.Do(req)
	if err != nil {
		return c.failure(fmt.Errorf("policy/opa: request failed: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.failure(fmt.Errorf("policy/opa: unexpected status %d", resp.StatusCode))
	}

	var out opaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return c.failure(fmt.Errorf("policy/opa: decoding response: %w", err))
	}

	// An undefined decision (no matching rule) comes back with result absent.
	// That is not an error, but it IS the absence of an allow, so it denies
	// unless FailOpen.
	if len(out.Result) == 0 || string(out.Result) == "null" {
		if c.FailOpen {
			return Allow()
		}
		return Deny("opa", "no policy decision matched this statement")
	}

	// Bare boolean: `allow := true` queried directly.
	var boolResult bool
	if err := json.Unmarshal(out.Result, &boolResult); err == nil {
		if boolResult {
			return Allow()
		}
		return Deny("opa", "denied by policy")
	}

	var obj opaResultObject
	if err := json.Unmarshal(out.Result, &obj); err != nil {
		return c.failure(fmt.Errorf("policy/opa: unrecognized result shape: %s", out.Result))
	}

	// Accept either polarity so a policy can be written as `allow` or `deny`
	// without the caller adapting. `denied` wins when both are present,
	// because an explicit denial is the safer reading.
	switch {
	case obj.Denied != nil && *obj.Denied:
		return Verdict{Denied: true, Message: messageOr(obj.Message, "denied by policy"), Rule: ruleOr(obj.Rule)}
	case obj.Denied != nil && !*obj.Denied:
		return Allow()
	case obj.Allow != nil && *obj.Allow:
		return Allow()
	case obj.Allow != nil && !*obj.Allow:
		return Verdict{Denied: true, Message: messageOr(obj.Message, "denied by policy"), Rule: ruleOr(obj.Rule)}
	}

	return c.failure(fmt.Errorf("policy/opa: result carried neither allow nor denied: %s", out.Result))
}

// failure applies the fail-open/fail-closed choice to an evaluation error.
func (c *OPAClient) failure(err error) Verdict {
	if c.FailOpen {
		return Verdict{Err: err}
	}
	return Verdict{
		Denied:  true,
		Message: "policy engine unavailable; denying",
		Rule:    "opa",
		Err:     err,
	}
}

func messageOr(msg, fallback string) string {
	if msg != "" {
		return msg
	}
	return fallback
}

func ruleOr(rule string) string {
	if rule != "" {
		return rule
	}
	return "opa"
}
