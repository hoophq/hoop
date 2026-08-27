// Package anthropic implements the Anthropic Messages API as an analyzer
// provider.
//
// It is hand-rolled against net/http rather than built on the official SDK,
// because the root module takes no dependency it can avoid and the request this
// package makes is one JSON POST. The SDK's value is breadth — streaming,
// files, batches — and none of it applies to a single tool-calling
// classification.
//
// Registering this package also registers the Vertex body encoder it shares
// with analyzer/vertex: Claude on Vertex is this same wire format with a
// different URL and a different auth header.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hoophq/hoop/hoopinspect/analyzer"
)

// Name is the config value that selects this provider.
const Name = "anthropic"

// DefaultEndpoint is Anthropic's Messages API.
const DefaultEndpoint = "https://api.anthropic.com/v1/messages"

// apiVersion is the Anthropic API version header value. It is pinned rather
// than tracking latest: a silent API revision must not change how statements
// are classified in a deployment nobody touched.
const apiVersion = "2023-06-01"

// defaultMaxTokens bounds the reply. A tool call with a title and a short
// explanation is a few hundred tokens; 1024 is slack, not a budget.
const defaultMaxTokens = 1024

func init() {
	analyzer.Register(Name, func(opts analyzer.Options) (analyzer.Provider, error) {
		if opts.Credential.IsZero() {
			return nil, fmt.Errorf("analyzer/anthropic: no api key configured")
		}
		if opts.Model == "" {
			return nil, fmt.Errorf("analyzer/anthropic: no model configured")
		}
		endpoint := opts.Endpoint
		if endpoint == "" {
			endpoint = DefaultEndpoint
		}
		return &Provider{
			endpoint:  endpoint,
			model:     opts.Model,
			key:       opts.Credential,
			maxTokens: pickMaxTokens(opts.MaxOutputTokens),
			client:    &http.Client{Timeout: 0}, // the caller's ctx owns the deadline
		}, nil
	})
}

func pickMaxTokens(n int) int {
	if n > 0 {
		return n
	}
	return defaultMaxTokens
}

// Provider classifies statements with the Anthropic Messages API.
type Provider struct {
	endpoint  string
	model     string
	key       analyzer.Secret
	maxTokens int
	client    *http.Client
}

// Name implements analyzer.Provider.
func (p *Provider) Name() string { return Name }

// Classify implements analyzer.Provider.
func (p *Provider) Classify(ctx context.Context, systemPrompt, content string) (*analyzer.Result, error) {
	body, err := json.Marshal(BuildRequest(p.model, p.maxTokens, systemPrompt, content, false))
	if err != nil {
		return nil, fmt.Errorf("analyzer/anthropic: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("analyzer/anthropic: building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("x-api-key", string(p.key.Bytes()))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analyzer/anthropic: %w", err)
	}
	defer resp.Body.Close()

	return ParseResponse("analyzer/"+Name, resp)
}

// --- wire format, shared with Vertex ---------------------------------------

// Request is the Messages API request body.
//
// It is exported, with the Vertex-only fields, so analyzer/vertex reuses this
// encoder instead of maintaining a second copy that drifts.
type Request struct {
	// Model is omitted on Vertex, where the model is in the URL path.
	Model string `json:"model,omitempty"`

	// AnthropicVersion is set ONLY on Vertex, which takes the version in
	// the body rather than as a header. Anthropic direct rejects it.
	AnthropicVersion string `json:"anthropic_version,omitempty"`

	MaxTokens  int        `json:"max_tokens"`
	System     string     `json:"system"`
	Messages   []Message  `json:"messages"`
	Tools      []Tool     `json:"tools"`
	ToolChoice ToolChoice `json:"tool_choice"`
}

// Message is one turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool is a callable the model may invoke.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"input_schema"`
}

// InputSchema is a tool's JSON Schema.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

// Property is one schema field.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ToolChoice forces the model to call a tool.
type ToolChoice struct {
	Type string `json:"type"`
}

// VertexAPIVersion is the value Vertex requires in anthropic_version.
const VertexAPIVersion = "vertex-2023-10-16"

// BuildRequest renders a classification request.
//
// forVertex switches the two transport differences: the model moves from the
// body to the URL, and the API version moves from a header into the body.
// Everything else — messages, tools, tool_choice — is identical, which is why
// one encoder serves both.
func BuildRequest(model string, maxTokens int, systemPrompt, content string, forVertex bool) Request {
	specs := analyzer.ToolSpecs()
	tools := make([]Tool, 0, len(specs))
	for _, s := range specs {
		tools = append(tools, Tool{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"title": {
						Type:        "string",
						Description: "Under 80 characters. Shown to the user when the statement is blocked. Never quote a value from the statement.",
					},
					"explanation": {
						Type:        "string",
						Description: "Why the statement carries this risk. Never quote a value from the statement.",
					},
				},
				Required: []string{"title", "explanation"},
			},
		})
	}

	req := Request{
		MaxTokens: maxTokens,
		System:    systemPrompt,
		Messages:  []Message{{Role: "user", Content: content}},
		Tools:     tools,
		// "any" forces a tool call. Without it the model may answer in
		// prose, and a classifier whose output shape is optional is a
		// parsing problem rather than an enum.
		ToolChoice: ToolChoice{Type: "any"},
	}
	if forVertex {
		req.AnthropicVersion = VertexAPIVersion
	} else {
		req.Model = model
	}
	return req
}

// response is the Messages API reply.
type response struct {
	Content []contentBlock `json:"content"`

	// StopReason distinguishes a safety refusal from a model that answered
	// in prose. A refusal returns no content blocks at all, so without this
	// the two are indistinguishable and both report "called no risk tool".
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// maxErrorBytes bounds how much of a failed response is read.
const maxErrorBytes = 4 << 10

// ParseResponse turns an HTTP response into a Result.
//
// Exported so analyzer/vertex reuses it: Vertex returns the same document.
//
// provider names the caller in every error. Without it a Vertex user reading
// "analyzer/anthropic: provider returned 403" goes looking for an anthropic
// block in a config that has none.
//
// A non-2xx never surfaces the provider's body. An LLM 4xx frequently echoes
// the request that caused it, so propagating that body would copy the
// statement — and whatever it contained — into the relay's logs and into the
// error an operator pastes into a ticket.
func ParseResponse(provider string, resp *http.Response) (*analyzer.Result, error) {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Drain a bounded amount so the connection can be reused, and
		// discard it.
		_, _ = io.CopyN(io.Discard, resp.Body, maxErrorBytes)
		return nil, fmt.Errorf("%s: provider returned %s", provider, resp.Status)
	}

	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: decoding response: %w", provider, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s: provider error: %s", provider, out.Error.Type)
	}

	for _, block := range out.Content {
		if block.Type != "tool_use" {
			continue
		}
		level, ok := analyzer.RiskForTool(block.Name)
		if !ok {
			continue
		}
		var args struct {
			Title       string `json:"title"`
			Explanation string `json:"explanation"`
		}
		// A malformed argument object is not fatal: the tool NAME already
		// carries the risk level, which is the part enforcement depends
		// on. Losing the title costs a less useful message, not a wrong
		// decision.
		_ = json.Unmarshal(block.Input, &args)
		return &analyzer.Result{
			RiskLevel:   level,
			Title:       args.Title,
			Explanation: args.Explanation,
		}, nil
	}
	// A safety-tuned model can decline to engage with the very payload most
	// worth classifying: "delete every customer" reads as a request for help
	// destroying data. The reply carries stop_reason "refusal" and no content.
	//
	// Reported separately because the operator response differs. "Called no
	// risk tool" points at the prompt or the tool schema; a refusal points at
	// nothing the operator can fix, and means this statement went unscored.
	// Under fail_open that is an ALLOW, which is why it must be legible in
	// the audit trail rather than folded into a generic parse failure.
	if out.StopReason == "refusal" {
		return nil, fmt.Errorf("%s: model refused to classify this statement", provider)
	}
	return nil, fmt.Errorf("%s: model called no risk tool (stop_reason %q)",
		provider, out.StopReason)
}
