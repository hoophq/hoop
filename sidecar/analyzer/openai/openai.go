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
		if opts.Credential.IsZero() {
			return nil, fmt.Errorf("analyzer/openai: no api key configured")
		}
		if opts.Model == "" {
			return nil, fmt.Errorf("analyzer/openai: no model configured")
		}
		endpoint := opts.Endpoint
		if endpoint == "" {
			endpoint = DefaultEndpoint
		}
		maxTokens := opts.MaxOutputTokens
		if maxTokens <= 0 {
			maxTokens = defaultMaxTokens
		}
		return &Provider{
			endpoint:  endpoint,
			model:     opts.Model,
			key:       opts.Credential,
			maxTokens: maxTokens,
			client:    &http.Client{},
		}, nil
	})
}

// Provider classifies statements with an OpenAI-compatible API.
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
		return nil, fmt.Errorf("analyzer/openai: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("analyzer/openai: building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+string(p.key.Bytes()))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analyzer/openai: %w", err)
	}
	defer resp.Body.Close()

	return ParseResponse("analyzer/"+Name, resp)
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

// Property is one schema field.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// BuildRequest renders a classification request.
//
// forVertex switches the spelling of the output limit. OpenAI deprecated
// max_tokens for max_completion_tokens, and its reasoning models reject the
// old name. Vertex documents only max_tokens for its open models. Llama on
// Vertex returns empty text when the request has no limit it reads, and
// empty text carries no verdict.
func BuildRequest(model string, maxTokens int, systemPrompt, content string, forVertex bool) Request {
	specs := analyzer.ToolSpecs()
	tools := make([]Tool, 0, len(specs))
	for _, s := range specs {
		tools = append(tools, Tool{
			Type: "function",
			Function: FunctionSpec{
				Name:        s.Name,
				Description: s.Description,
				Parameters: Parameters{
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
			},
		})
	}

	req := Request{
		Model: model,
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
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

const maxErrorBytes = 4 << 10

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
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
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
