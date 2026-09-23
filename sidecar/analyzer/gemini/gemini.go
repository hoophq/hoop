// Package gemini implements Google's Gemini generateContent API as an
// analyzer provider, authenticated with an API key.
//
// Google serves Gemini from two hosts that take the same request body and
// return the same reply. The Gemini Developer API (generativelanguage) bills
// a Google account and has no project. The Vertex AI Gemini API
// (aiplatform) bills a GCP project and sits inside its IAM, VPC Service
// Controls and region policy. The `api` key in the config picks the host;
// the encoder does not care.
//
// The API key is the only credential this package knows. An operator with a
// GCP identity (a service-account key, Workload Identity, an attached
// service account) uses analyzer/vertex with `publisher: google`, which
// mints a bearer token and reuses BuildRequest and ParseResponse from here.
// The split follows the module boundary: minting tokens needs
// golang.org/x/oauth2, and the root module takes no dependency it can avoid.
//
// Hand-rolled against net/http. See analyzer/anthropic for why.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Name is the config value that selects this provider.
const Name = "gemini"

// KeyAPI is the extra key that picks the host. Values are APIDeveloper and
// APIVertex; empty means APIDeveloper.
const KeyAPI = "api"

// API values for KeyAPI.
const (
	// APIDeveloper is the Gemini Developer API on generativelanguage.
	APIDeveloper = "developer"

	// APIVertex is the Vertex AI Gemini API on aiplatform, reached with a
	// Google Cloud API key. The key is bound to a project, so the URL
	// carries no project or location.
	APIVertex = "vertex"
)

// defaultMaxTokens bounds the reply. A tool call with a title and a short
// explanation is a few hundred tokens; 1024 is slack, not a budget.
const defaultMaxTokens = 1024

func init() {
	analyzer.Register(Name, func(opts analyzer.Options) (analyzer.Provider, error) {
		if opts.Credential.IsZero() {
			return nil, fmt.Errorf("analyzer/gemini: no api key configured")
		}
		if opts.Model == "" {
			return nil, fmt.Errorf("analyzer/gemini: no model configured")
		}
		api := strings.TrimSpace(opts.Extra[KeyAPI])
		if api == "" {
			api = APIDeveloper
		}
		endpoint := opts.Endpoint
		if endpoint == "" {
			var err error
			endpoint, err = DefaultEndpoint(api, opts.Model)
			if err != nil {
				return nil, err
			}
		}
		maxTokens := opts.MaxOutputTokens
		if maxTokens <= 0 {
			maxTokens = defaultMaxTokens
		}
		return &Provider{
			endpoint:  endpoint,
			key:       opts.Credential,
			maxTokens: maxTokens,
			client:    &http.Client{Timeout: 0}, // the caller's ctx owns the deadline
		}, nil
	})
}

// DefaultEndpoint builds the generateContent URL for a model on one of the
// two hosts.
//
// It returns an error for an unknown api value at config load, where the
// operator is watching. Falling back to a default would send a Vertex
// operator's key to generativelanguage, and the resulting 400 reads like a
// bad key.
func DefaultEndpoint(api, model string) (string, error) {
	switch api {
	case APIDeveloper:
		return "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":generateContent", nil
	case APIVertex:
		return "https://aiplatform.googleapis.com/v1/publishers/google/models/" + model + ":generateContent", nil
	}
	return "", fmt.Errorf("analyzer/gemini: unknown api %q (want %q or %q)", api, APIDeveloper, APIVertex)
}

// Provider classifies statements with the Gemini API under an API key.
type Provider struct {
	endpoint  string
	key       analyzer.Secret
	maxTokens int
	client    *http.Client
}

// Name implements analyzer.Provider.
func (p *Provider) Name() string { return Name }

// Classify implements analyzer.Provider.
func (p *Provider) Classify(ctx context.Context, systemPrompt, content string) (*analyzer.Result, error) {
	body, err := json.Marshal(BuildRequest(p.maxTokens, systemPrompt, content))
	if err != nil {
		return nil, fmt.Errorf("analyzer/gemini: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("analyzer/gemini: building request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	// The header, never the `?key=` query form Google's quickstarts show.
	// A query string lands in access logs and in /config, which prints the
	// endpoint; a header stays in the request.
	req.Header.Set("x-goog-api-key", string(p.key.Bytes()))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analyzer/gemini: %w", err)
	}
	defer resp.Body.Close()

	return ParseResponse("analyzer/"+Name, resp)
}

// --- wire format, shared with Vertex ---------------------------------------

// Request is the generateContent request body.
//
// Exported so analyzer/vertex reuses this encoder instead of keeping a second
// copy that drifts. The model is never in the body: every Gemini URL names it
// in the path.
type Request struct {
	SystemInstruction Content          `json:"systemInstruction"`
	Contents          []Content        `json:"contents"`
	Tools             []Tool           `json:"tools"`
	ToolConfig        ToolConfig       `json:"toolConfig"`
	GenerationConfig  GenerationConfig `json:"generationConfig"`
}

// Content is one turn, or the system instruction, which has no role.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// Part is one piece of a turn. Requests carry Text; replies carry
// FunctionCall.
type Part struct {
	Text         string        `json:"text,omitempty"`
	FunctionCall *FunctionCall `json:"functionCall,omitempty"`
}

// FunctionCall is the model's call of one risk tool. Args is a JSON object,
// not a string: Gemini differs from OpenAI here.
type FunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Tool groups the function declarations the model may call.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations"`
}

// FunctionDeclaration is one callable.
type FunctionDeclaration struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  Schema `json:"parameters"`
}

// Schema is the OpenAPI subset Gemini accepts for parameters. Type values
// are the upper-case enum names (OBJECT, STRING): Vertex documents those,
// and both hosts accept them.
type Schema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Property is one schema field.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ToolConfig forces a function call.
type ToolConfig struct {
	FunctionCallingConfig FunctionCallingConfig `json:"functionCallingConfig"`
}

// FunctionCallingConfig is the tool-choice setting.
type FunctionCallingConfig struct {
	Mode string `json:"mode"`
}

// GenerationConfig bounds the reply.
type GenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

// BuildRequest renders a classification request.
func BuildRequest(maxTokens int, systemPrompt, content string) Request {
	specs := analyzer.ToolSpecs()
	decls := make([]FunctionDeclaration, 0, len(specs))
	for _, s := range specs {
		decls = append(decls, FunctionDeclaration{
			Name:        s.Name,
			Description: s.Description,
			Parameters: Schema{
				Type: "OBJECT",
				Properties: map[string]Property{
					"title": {
						Type:        "STRING",
						Description: "Under 80 characters. Shown to the user when the statement is blocked. Never quote a value from the statement.",
					},
					"explanation": {
						Type:        "STRING",
						Description: "Why the statement carries this risk. Never quote a value from the statement.",
					},
				},
				Required: []string{"title", "explanation"},
			},
		})
	}

	return Request{
		SystemInstruction: Content{Parts: []Part{{Text: systemPrompt}}},
		Contents:          []Content{{Role: "user", Parts: []Part{{Text: content}}}},
		Tools:             []Tool{{FunctionDeclarations: decls}},
		// ANY forces a function call. Without it the model may answer in
		// prose, and a classifier whose output shape is optional is a
		// parsing problem rather than an enum.
		ToolConfig:       ToolConfig{FunctionCallingConfig: FunctionCallingConfig{Mode: "ANY"}},
		GenerationConfig: GenerationConfig{MaxOutputTokens: maxTokens},
	}
}

// response is the generateContent reply.
type response struct {
	Candidates []struct {
		Content Content `json:"content"`

		// FinishReason distinguishes a safety stop from a model that
		// answered in prose. Both leave no functionCall part, so without
		// it the two read the same.
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`

	// PromptFeedback is set when the PROMPT was blocked before generation.
	// There are then no candidates at all.
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`

	Error *struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

// maxErrorBytes bounds how much of a failed response is read.
const maxErrorBytes = 4 << 10

// ParseResponse turns an HTTP response into a Result.
//
// Exported so analyzer/vertex reuses it: Vertex returns the same document.
//
// provider names the caller in every error. Without it a Vertex user reading
// "analyzer/gemini: provider returned 403" goes looking for a gemini block in
// a config that has none.
//
// A non-2xx never surfaces the provider's body. An LLM 4xx echoes the request
// that caused it often enough that propagating the body would copy the
// statement, and whatever it contained, into the relay's logs and into the
// error an operator pastes into a ticket. The same rule keeps Error.Message
// out of the returned error: Google's message can quote the offending field.
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
		return nil, fmt.Errorf("%s: provider error: %s", provider, out.Error.Status)
	}

	for _, cand := range out.Candidates {
		for _, part := range cand.Content.Parts {
			if part.FunctionCall == nil {
				continue
			}
			level, ok := analyzer.RiskForTool(part.FunctionCall.Name)
			if !ok {
				continue
			}
			var args struct {
				Title       string `json:"title"`
				Explanation string `json:"explanation"`
			}
			// A malformed argument object is not fatal: the tool NAME
			// carries the risk level, which is the part enforcement
			// depends on. Losing the title costs a less useful message,
			// not a wrong decision.
			_ = json.Unmarshal(part.FunctionCall.Args, &args)
			return &analyzer.Result{
				RiskLevel:   level,
				Title:       args.Title,
				Explanation: args.Explanation,
			}, nil
		}
	}

	// A safety-tuned model can decline the payload most worth classifying:
	// "delete every customer" reads as a request for help destroying data.
	// Gemini reports that two ways. A blocked prompt returns
	// promptFeedback.blockReason and no candidates. A blocked reply returns
	// a candidate with a safety finishReason and no parts.
	//
	// Reported apart from "called no risk tool" because the operator
	// response differs. The latter points at the prompt or the tool
	// schema; a refusal points at nothing the operator can fix, and means
	// this statement went unscored. Under fail_open that is an ALLOW, so it
	// must be legible in the audit trail rather than folded into a generic
	// parse failure.
	if out.PromptFeedback != nil && out.PromptFeedback.BlockReason != "" {
		return nil, fmt.Errorf("%s: model refused to classify this statement (block_reason %q)",
			provider, out.PromptFeedback.BlockReason)
	}
	finish := ""
	if len(out.Candidates) > 0 {
		finish = out.Candidates[0].FinishReason
	}
	switch finish {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		return nil, fmt.Errorf("%s: model refused to classify this statement (finish_reason %q)",
			provider, finish)
	}
	return nil, fmt.Errorf("%s: model called no risk tool (finish_reason %q)", provider, finish)
}
