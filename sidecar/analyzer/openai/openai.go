// Package openai implements the OpenAI Chat Completions API as an analyzer
// provider.
//
// It also serves any OpenAI-compatible endpoint — Azure OpenAI, vLLM,
// Ollama's compatibility layer, a gateway in front of one — by setting the
// endpoint explicitly. That is the whole reason this package exists beside
// the Anthropic one: a deployment that cannot reach Anthropic usually has
// something speaking this dialect.
//
// Hand-rolled against net/http. See analyzer/anthropic for why.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Name is the config value that selects this provider.
const Name = "openai"

// DefaultEndpoint is OpenAI's Chat Completions API.
const DefaultEndpoint = "https://api.openai.com/v1/chat/completions"

const defaultMaxTokens = 1024

func init() {
	analyzer.Register(Name, func(opts analyzer.Options) (analyzer.Provider, error) {
		p, err := New(Name, opts)
		if err != nil {
			return nil, err
		}
		return p, nil
	})
}

// New builds a provider that reports itself, in Name and in every error, as
// name. A provider that speaks this dialect under its own name (hoop) uses
// it, so an operator reading an error is sent to the right config block.
func New(name string, opts analyzer.Options) (*Provider, error) {
	if opts.Credential.IsZero() {
		return nil, fmt.Errorf("analyzer/%s: no api key configured", name)
	}
	if opts.Model == "" {
		return nil, fmt.Errorf("analyzer/%s: no model configured", name)
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	maxTokens := opts.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	if err := opts.Sampling.Unsupported("analyzer/"+name, UnsupportedSampling...); err != nil {
		return nil, err
	}
	return &Provider{
		name:      name,
		endpoint:  endpoint,
		model:     opts.Model,
		key:       opts.Credential,
		maxTokens: maxTokens,
		sampling:  opts.Sampling,
		client:    opts.Client(),
	}, nil
}

// Provider classifies statements with an OpenAI-compatible API.
type Provider struct {
	name      string
	endpoint  string
	model     string
	key       analyzer.Secret
	maxTokens int
	sampling  analyzer.Sampling
	client    *http.Client
}

// Name implements analyzer.Provider.
func (p *Provider) Name() string { return p.name }

// Classify implements analyzer.Provider.
func (p *Provider) Classify(ctx context.Context, systemPrompt, content string) (*analyzer.Result, error) {
	resp, err := p.send(ctx, BuildRequest(p.model, p.maxTokens, p.sampling, systemPrompt, content, false))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ParseResponse("analyzer/"+p.name, resp)
}

// Decide implements analyzer.ToolCaller.
func (p *Provider) Decide(ctx context.Context, systemPrompt, content string, tools []analyzer.ToolSpec) ([]analyzer.ToolCall, error) {
	resp, err := p.send(ctx, BuildToolRequest(p.model, p.maxTokens, p.sampling, systemPrompt, content, tools, false))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return ParseToolCalls("analyzer/"+p.name, resp)
}

// send posts one request body. The caller closes the response body.
func (p *Provider) send(ctx context.Context, request Request) (*http.Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("analyzer/%s: encoding request: %w", p.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("analyzer/%s: building request: %w", p.name, err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+string(p.key.Bytes()))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analyzer/%s: %w", p.name, err)
	}
	return resp, nil
}

// --- wire format, shared with Vertex ---------------------------------------

// Request is the Chat Completions request body.
//
// analyzer/vertex reuses this encoder for the Model Garden open models on
// Vertex's OpenAI-compatible endpoint, so no second copy can drift from it.
type Request struct {
	Model string `json:"model"`

	// BuildRequest fills one of these two limits: the one its target reads.
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	MaxTokens           int `json:"max_tokens,omitempty"`

	// Sampling, omitted when unset so the model default applies. Chat
	// Completions has no top_k; see UnsupportedSampling.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`

	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools"`
	ToolChoice string    `json:"tool_choice"`
}

// Message is one turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool is a callable the model may invoke.
type Tool struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec names a tool and its arguments.
type FunctionSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  Parameters `json:"parameters"`
}

// Parameters is a tool's JSON Schema.
type Parameters struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

// Property is one schema field. Items describes an array's elements.
type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Enum        []string  `json:"enum,omitempty"`
	Items       *Property `json:"items,omitempty"`
}

// property renders one tool argument, its array elements included.
func property(p analyzer.ToolProperty) Property {
	out := Property{Type: p.Type, Description: p.Description, Enum: p.Enum}
	if p.Items != nil {
		items := property(*p.Items)
		out.Items = &items
	}
	return out
}

// UnsupportedSampling names the sampling parameters Chat Completions has no
// field for. A provider speaking this encoder refuses them at construction.
var UnsupportedSampling = []string{"top_k"}

// BuildRequest renders a classification request.
//
// forVertex switches the spelling of the output limit. OpenAI deprecated
// max_tokens for max_completion_tokens, and its reasoning models reject the
// old name. Vertex documents only max_tokens for its open models. Llama on
// Vertex returns empty text when the request has no limit it reads, and
// empty text carries no verdict.
func BuildRequest(model string, maxTokens int, s analyzer.Sampling, systemPrompt, content string, forVertex bool) Request {
	return BuildToolRequest(model, maxTokens, s, systemPrompt, content, analyzer.ToolSpecs(), forVertex)
}

// BuildToolRequest renders a request that offers tools and requires a call.
// BuildRequest is this with the risk tools.
func BuildToolRequest(model string, maxTokens int, s analyzer.Sampling, systemPrompt, content string,
	specs []analyzer.ToolSpec, forVertex bool) Request {
	tools := make([]Tool, 0, len(specs))
	for _, s := range specs {
		props := make(map[string]Property, len(s.Input.Properties))
		for name, p := range s.Input.Properties {
			props[name] = property(p)
		}
		required := s.Input.Required
		if required == nil {
			required = []string{}
		}
		tools = append(tools, Tool{
			Type: "function",
			Function: FunctionSpec{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  Parameters{Type: "object", Properties: props, Required: required},
			},
		})
	}

	req := Request{
		Model:       model,
		Temperature: s.Temperature,
		TopP:        s.TopP,
		Seed:        s.Seed,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: content},
		},
		Tools: tools,
		// "required" is OpenAI's spelling of Anthropic's "any": call some
		// tool, your choice which. Without it the model may reply in
		// prose and the verdict becomes a parsing problem.
		ToolChoice: "required",
	}
	if forVertex {
		req.MaxTokens = maxTokens
	} else {
		req.MaxCompletionTokens = maxTokens
	}
	return req
}

type response struct {
	Choices []struct {
		Message struct {
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`

			// Refusal is set when the model declined to answer.
			Refusal string `json:"refusal"`
		} `json:"message"`

		// FinishReason "content_filter" means a safety stop.
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ParseResponse turns an HTTP response into a Result.
//
// analyzer/vertex reuses it, because Vertex's OpenAI-compatible endpoint
// returns the same document. provider names the caller in each error, so a
// Vertex operator does not go looking for an openai block their config lacks.
//
// As with Anthropic, a non-2xx body is drained and discarded rather than
// propagated: these APIs echo the offending request often enough that
// forwarding the body would copy the statement into the relay's logs.
func ParseResponse(provider string, resp *http.Response) (*analyzer.Result, error) {
	out, err := decodeResponse(provider, resp)
	if err != nil {
		return nil, err
	}

	for _, choice := range out.Choices {
		for _, call := range choice.Message.ToolCalls {
			level, ok := analyzer.RiskForTool(call.Function.Name)
			if !ok {
				continue
			}
			var args struct {
				Title       string `json:"title"`
				Explanation string `json:"explanation"`
			}
			// Arguments arrive as a JSON string, not an object. A
			// malformed one costs the title, not the verdict: the
			// function name already carries the risk level.
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
			return &analyzer.Result{
				RiskLevel:   level,
				Title:       args.Title,
				Explanation: args.Explanation,
			}, nil
		}
	}
	return nil, fmt.Errorf("%s: model called no risk tool", provider)
}

// decodeResponse reads a 2xx reply, or reports the failure without the body.
func decodeResponse(provider string, resp *http.Response) (response, error) {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return response{}, analyzer.ResponseError(provider, resp)
	}
	var out response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return response{}, fmt.Errorf("%s: decoding response: %w", provider, err)
	}
	if out.Error != nil {
		return response{}, fmt.Errorf("%s: provider error: %s", provider, out.Error.Type)
	}
	return out, nil
}

// ParseToolCalls returns every tool call of a reply, in order. Arguments
// arrive as a JSON string; one that is not JSON comes back with nil Args.
// A refusal, a content filter or a reply cut off at the token limit is an
// error, with or without calls; a reply with no call is not. Exported so
// analyzer/vertex reuses it.
func ParseToolCalls(provider string, resp *http.Response) ([]analyzer.ToolCall, error) {
	out, err := decodeResponse(provider, resp)
	if err != nil {
		return nil, err
	}
	// A refusal or a content filter on any choice wins over any call:
	// a tool call beside one is not a decision.
	for _, choice := range out.Choices {
		if choice.Message.Refusal != "" || choice.FinishReason == "content_filter" {
			return nil, fmt.Errorf("%s: model refused to answer", provider)
		}
		// A choice cut off at the limit may end inside a call: no decision.
		if choice.FinishReason == "length" {
			return nil, fmt.Errorf("%s: model reply was cut off (finish_reason %q)", provider, choice.FinishReason)
		}
	}
	var calls []analyzer.ToolCall
	for _, choice := range out.Choices {
		for _, call := range choice.Message.ToolCalls {
			c := analyzer.ToolCall{Name: call.Function.Name}
			if args := []byte(call.Function.Arguments); json.Valid(args) {
				c.Args = args
			}
			calls = append(calls, c)
		}
	}
	return calls, nil
}
