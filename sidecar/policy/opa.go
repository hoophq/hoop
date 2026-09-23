package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// OPAClient evaluates statements against an Open Policy Agent Data API
// endpoint.
//
// # Shape of the contract
//
// InfoSec teams that already run OPA will not adopt a second policy system.
// So sidecar does NOT own policy here; it owns the input document.
// Envoy's postgres_proxy can hand OPA a resource name and an operation verb.
// This client hands it the statement text, the normalized operation, the
// table list and the protocol, in one shape for four protocols.
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
// A bare boolean result also decodes, so `data.hoop.allow` written as a
// simple rule works without a wrapper object.
//
// # Two phases
//
// A lane running an expensive producer consults OPA twice, and Phase says
// which call this is. PhaseGate runs BEFORE the producers and answers "is
// this worth running them", by returning `request` alongside its decision.
// PhaseDecide runs AFTER and sees what they established in `input.findings`,
// so the block/allow choice is Rego's rather than a table in YAML.
//
// An empty Phase is the single-call arrangement: one decision, no
// `input.findings`, exactly what every lane did before producers reported.
//
// # Responses
//
// Every statement of an exchange reaches OPA, in both directions, and on a
// streaming lane that is one round trip PER RESPONSE MESSAGE: a 50k-row
// BigQuery read is 50k calls in series on the response path, seconds of
// latency for a policy that only ever looked at the request. Two switches
// remove them, and both leave the local rules, the audit trail and masking
// exactly as they were; only the round trip goes.
//
//   - SkipResponses, set by the operator: this lane's OPA never sees a
//     FromServer statement.
//   - `responses: false` in the result of a FromClient decision: the policy
//     itself declares it has no opinion on THIS exchange's response side,
//     per method, per table, per actor — whatever the Rego keys on. It is
//     recorded on the EvalContext (SkipResponses), and a caller that owns
//     exactly one exchange (the gRPC lane's per-RPC gate) feeds it back as
//     a Requested veto on the response statements that follow. Chain's own
//     vocabulary: `request` asks a producer to run or not; this asks the
//     same of OPA. A caller serving a whole connection cannot pair
//     responses with requests and ignores it; the request's audit record
//     then carries AnnotationResponsesUnscoped.
//
// A skipped statement carries AnnotationOPASkipped onto its audit record,
// so a row that says "allowed, no rule" can be told apart from one OPA
// actually allowed.
type OPAClient struct {
	// URL is the full decision endpoint, e.g.
	// http://opa:8181/v1/data/hoop/inspect
	URL string

	// HTTPClient serves the requests. When nil, the client builds one with
	// Timeout on first use.
	HTTPClient *http.Client

	// Timeout bounds a single decision. OPA sits on the data path, so keep
	// this small: a slow policy engine must fail the request rather than
	// hang the connection. Defaults to 2s.
	Timeout time.Duration

	// Phase identifies this call when a lane consults OPA twice. Empty for
	// the single-call arrangement.
	//
	// It reaches Rego as input.phase, so one endpoint answers both: a
	// policy that ignores the field behaves identically in either call,
	// which is what makes the gate opt-in rather than a rewrite.
	Phase Phase

	// FailOpen allows the statement when OPA cannot be reached or returns a
	// malformed response. Default false (deny).
	FailOpen bool

	// Context carries extra fields into input alongside the statement: the
	// authenticated user, the hoop connection name, a correlation id. The
	// client reads it once per evaluation, so put per-connection facts here
	// and per-statement facts in the Statement itself.
	Context map[string]string

	// SkipResponses answers every FromServer statement with an allow and
	// no round trip. For a lane whose policy is written against requests,
	// where a response message costs a call that can only ever say yes.
	SkipResponses bool
}

// Phase names which of a two-phase lane's OPA calls this is. It reaches Rego
// as input.phase.
type Phase string

const (
	// PhaseGate is the pre-analysis decision. It may request analysis.
	PhaseGate Phase = "gate"

	// PhaseDecide is the post-analysis decision. It reads input.ai.
	PhaseDecide Phase = "decide"
)

// opaRequest is the wire body the client posts to OPA.
type opaRequest struct {
	Input opaInput `json:"input"`
}

// opaInput is the document a Rego policy sees as `input`.
//
// Field names are snake_case and stable: they form a public contract with
// whoever writes the Rego, and renaming one silently breaks their policy.
type opaInput struct {
	Protocol  string   `json:"protocol"`
	Direction string   `json:"direction"`
	Statement string   `json:"statement"`
	Operation string   `json:"operation"`
	Tables    []string `json:"tables,omitempty"`

	// Effects is every operation the statement performs, and Relations
	// says which objects it writes versus reads.
	//
	// They are what `operation` and `tables` could not express. A statement
	// is one verb only if you read its first word: a data-modifying CTE
	// both deletes and selects, and a flat name list cannot separate
	// `INSERT INTO staging SELECT * FROM customers` from
	// `DELETE FROM customers`. Write new rules against these two:
	//
	//	some r in input.relations
	//	r.access == "write"
	//	r.name == "customers"
	//
	// `operation` remains the worst single effect, so a rule keyed on it
	// still fires, and `tables` remains the flattened names.
	Effects   []inspect.Operation `json:"effects,omitempty"`
	Relations []inspect.Relation  `json:"relations,omitempty"`
	Database  string              `json:"database,omitempty"`
	HTTP      *inspect.HTTPDetail `json:"http,omitempty"`
	Metadata  map[string]string   `json:"metadata,omitempty"`
	Context   map[string]string   `json:"context,omitempty"`

	// Phase is "gate", "decide", or absent on a single-call lane.
	Phase string `json:"phase,omitempty"`

	// Findings is what each producer established, keyed by its source.
	// Absent on the gate phase and on any lane where nothing reported.
	//
	// A source that ran and could NOT answer still appears, carrying a
	// status and no values, because "no answer" is the case a policy most
	// needs to see and the case an absent key hides.
	Findings map[string]Finding `json:"findings,omitempty"`
}

// opaResponse models OPA's Data API reply. Result is json.RawMessage,
// deliberately, so both an object and a bare boolean decode.
type opaResponse struct {
	Result json.RawMessage `json:"result"`
}

type opaResultObject struct {
	Allow   *bool  `json:"allow"`
	Denied  *bool  `json:"denied"`
	Message string `json:"message"`
	Rule    string `json:"rule"`

	// Request asks for producers to run, keyed by source. Read only on the
	// gate phase: true runs one its own configuration would have skipped,
	// false vetoes one it would have run.
	Request map[string]bool `json:"request"`

	// Responses, when false on a FromClient decision, declares the policy
	// has no opinion on the response side of this exchange. Absent or true
	// changes nothing. Read on every phase, because whichever call the
	// author keyed the rule on is the one that knows.
	Responses *bool `json:"responses"`
}

// AnnotationOPASkipped marks an audit record whose statement OPA never saw.
// The value names why: "responses" for the operator's SkipResponses or the
// policy's own `responses: false`.
const AnnotationOPASkipped = "opa.skipped"

// AnnotationResponsesUnscoped marks a request whose `responses: false` the
// caller could not honor, because it serves a whole connection and cannot
// tell which response answers which request. The value names the scope it
// had: "connection".
const AnnotationResponsesUnscoped = "opa.responses_unscoped"

// Evaluate implements Evaluator.
//
// It uses context.Background with the client's Timeout. Use EvaluateContext to
// propagate a caller's cancellation.
func (c *OPAClient) Evaluate(stmt inspect.Statement) Verdict {
	return c.EvaluateContext(context.Background(), stmt)
}

// EvaluateWith implements ContextualEvaluator.
//
// On the decide phase it sends what the producers established as
// input.findings. On the gate phase it writes ec.Requested from the policy's
// answer. Both directions travel in-process: a producer has no connection to
// OPA and OPA has none to any producer.
func (c *OPAClient) EvaluateWith(stmt inspect.Statement, ec *EvalContext) Verdict {
	return c.evaluate(context.Background(), stmt, ec)
}

// EvaluateContext evaluates under a caller-supplied context.
func (c *OPAClient) EvaluateContext(ctx context.Context, stmt inspect.Statement) Verdict {
	return c.evaluate(ctx, stmt, nil)
}

// findingsFor renders the producers' findings for the decide phase.
//
// Returns nil on any other phase and on a lane where nothing reported: a lane
// with no producers must not grow an input.findings field, or every Rego
// policy has to distinguish "nothing runs here" from "everything failed".
//
// A producer that ran and could not answer DOES appear. Whether its values
// are trustworthy is Status's job, not the caller's, so this copies the map
// through untouched rather than filtering on status: a producer that sets
// values beside a degraded status meant to.
func (c *OPAClient) findingsFor(ec *EvalContext) map[string]Finding {
	if c.Phase != PhaseDecide || ec == nil || len(ec.Findings) == 0 {
		return nil
	}
	return ec.Findings
}

// contextFor merges the client's static Context with the per-connection facts
// the caller seeded on the evaluation context.
//
// The seeded facts win. Context on the client is a library caller's fixed
// deployment labels; the ones on ec name the actor who ran THIS statement,
// and a policy asking "who is this" means the second.
//
// Neither side is copied when the other is empty, which is the common case:
// this runs once per statement per phase, on the data path.
func (c *OPAClient) contextFor(ec *EvalContext) map[string]string {
	if ec == nil || len(ec.Context) == 0 {
		return c.Context
	}
	if len(c.Context) == 0 {
		return ec.Context
	}
	out := make(map[string]string, len(c.Context)+len(ec.Context))
	for k, v := range c.Context {
		out[k] = v
	}
	for k, v := range ec.Context {
		out[k] = v
	}
	return out
}

func (c *OPAClient) evaluate(ctx context.Context, stmt inspect.Statement, ec *EvalContext) Verdict {
	// Two ways this statement never reaches the endpoint, checked before
	// the input document is even built. The operator's switch covers the
	// response side of the lane; a Requested veto covers whatever the
	// exchange's owner decided, which today is the policy's own
	// `responses: false` fed back by the gate (see EvalContext.SkipResponses)
	// and could as well be a gate-phase `request: {"opa": false}`.
	if c.SkipResponses && stmt.Direction == inspect.FromServer {
		return c.skipped()
	}
	if want, stated := ec.WantsRun(SourceOPA); stated && !want {
		return c.skipped()
	}
	body, err := json.Marshal(opaRequest{Input: opaInput{
		Protocol:  string(stmt.Protocol),
		Direction: string(stmt.Direction),
		Statement: stmt.Text,
		Operation: string(stmt.Operation),
		Tables:    stmt.Tables,
		Effects:   stmt.Effects,
		Relations: stmt.Relations,
		Database:  stmt.Database,
		HTTP:      stmt.HTTP,
		Metadata:  stmt.Metadata,
		Context:   c.contextFor(ec),
		Phase:     string(c.Phase),
		Findings:  c.findingsFor(ec),
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
	// OPA did not fail, yet nothing allowed the statement either, so it
	// denies unless FailOpen.
	//
	// The gate phase is the exception, and it has to be. A gate is an
	// optional pre-filter over a policy someone already wrote; making its
	// absence deny would mean adding a two-phase lane silently blocks every
	// statement until the Rego author writes a second rule they never asked
	// for. Undefined there means "no opinion": allow, request nothing, and
	// let the decide phase and the analyzer's own trigger carry on.
	if len(out.Result) == 0 || string(out.Result) == "null" {
		if c.FailOpen || c.Phase == PhaseGate {
			return Allow()
		}
		return c.deny("no policy decision matched this statement", "")
	}

	// Bare boolean: `allow := true` queried directly.
	var boolResult bool
	if err := json.Unmarshal(out.Result, &boolResult); err == nil {
		if boolResult {
			return Allow()
		}
		return c.deny("denied by policy", "")
	}

	var obj opaResultObject
	if err := json.Unmarshal(out.Result, &obj); err != nil {
		return c.failure(fmt.Errorf("policy/opa: unrecognized result shape: %s", out.Result))
	}

	// The gate's answer to "is this worth running" travels back through the
	// chain rather than through the verdict, because it is not a decision
	// about this statement's fate. It is recorded even when the gate
	// denies, so a policy that both refuses and requests a producer is not
	// silently half-honored.
	if c.Phase == PhaseGate && ec != nil && len(obj.Request) > 0 {
		if ec.Requested == nil {
			ec.Requested = make(map[string]bool, len(obj.Request))
		}
		for source, want := range obj.Request {
			ec.Requested[source] = want
		}
	}

	// The policy's word that the response side of this exchange is of no
	// interest to it. Recorded on a FromClient decision only: a response
	// statement saying it would be answering a question already asked.
	// Recorded whether or not this statement was denied, for the same
	// reason `request` is — although a denied request has no responses.
	if stmt.Direction == inspect.FromClient && ec != nil && obj.Responses != nil && !*obj.Responses {
		ec.DeclineResponses(SourceOPA)
	}

	// Either polarity decodes, so a Rego policy written as `allow` or as
	// `deny` works without you adapting the caller. `denied` wins when both
	// are present, because an explicit denial is the safer reading.
	switch {
	case obj.Denied != nil && *obj.Denied:
		return c.deny(messageOr(obj.Message, "denied by policy"), obj.Rule)
	case obj.Denied != nil && !*obj.Denied:
		return Allow()
	case obj.Allow != nil && *obj.Allow:
		return Allow()
	case obj.Allow != nil && !*obj.Allow:
		return c.deny(messageOr(obj.Message, "denied by policy"), obj.Rule)
	}

	return c.failure(fmt.Errorf("policy/opa: result carried neither allow nor denied: %s", out.Result))
}

// skipped is the verdict for a statement OPA was not asked about. An allow,
// because nothing objected, annotated so the trail does not read as one.
func (c *OPAClient) skipped() Verdict {
	return Verdict{Annotations: map[string]string{AnnotationOPASkipped: "responses"}}
}

// deny builds an OPA denial. rule is the policy's own name for the
// decision, when it gave one.
func (c *OPAClient) deny(msg, rule string) Verdict {
	return Verdict{Denied: true, Message: msg, Rule: ruleOr(rule), Source: SourceOPA}
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
		Source:  SourceOPA,
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
