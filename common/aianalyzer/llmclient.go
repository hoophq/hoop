// Package aianalyzer holds the provider-agnostic AI session risk classifier.
//
// It lives in common (not libhoop) so it ships identically in the OSS and
// enterprise builds: the gateway calls it directly for exec-path analysis, and
// the agent constructs it and injects it into the libhoop HTTP proxy (which
// cannot import common) through a small adapter. The package depends only on
// the LLM provider SDKs and the standard library — never on gateway/ or
// agent/ — so it stays a leaf dependency.
package aianalyzer

import (
	"context"
	"fmt"
)

// Role represents the role of a participant in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message is a single turn in a conversation.
type Message struct {
	Role    Role
	Content string
}

// ToolProperty describes a single property within a tool's input JSON Schema.
type ToolProperty struct {
	Type        string
	Description string
	Enum        []string
}

// ToolInputSchema is the JSON Schema for a tool's parameters.
type ToolInputSchema struct {
	Properties map[string]ToolProperty
	Required   []string
}

// Tool defines a callable function the model may invoke.
type Tool struct {
	Name        string
	Description string
	InputSchema ToolInputSchema
	Strict      bool
}

// ToolCall is a tool invocation returned in the model response.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolChoice constants for ChatRequest.ToolChoice.
// An empty string leaves the choice to the provider default ("auto" when tools are present).
// Any value not matching a constant is treated as a specific tool name to force.
const (
	ToolChoiceAuto = "auto" // model decides whether to call a tool
	ToolChoiceAny  = "any"  // model must call at least one tool (Anthropic); mapped to "required" for OpenAI
	ToolChoiceNone = "none" // model must not call any tool
)

// OutputFormatType controls how the model formats its output.
type OutputFormatType string

const (
	OutputFormatText       OutputFormatType = "text"        // plain text (default)
	OutputFormatJSONObject OutputFormatType = "json_object" // free-form valid JSON (no schema)
	OutputFormatJSONSchema OutputFormatType = "json_schema" // JSON conforming to a schema
)

// JSONSchemaDefinition holds the schema for structured output.
type JSONSchemaDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
	Strict      bool
}

// ResponseFormat controls the output format of the model's response.
// When Type is OutputFormatJSONSchema, JSONSchema must be set. For Anthropic,
// OutputFormatJSONSchema is implemented via tool-forcing.
type ResponseFormat struct {
	Type       OutputFormatType
	JSONSchema *JSONSchemaDefinition
}

// ChatRequest holds the parameters for a chat completion request.
//
//   - Model overrides the provider's default model when non-empty.
//   - MaxTokens defaults to 4096 when zero.
//   - SystemPrompt is placed according to each provider's convention.
//   - ToolChoice is only applied when Tools is non-empty; see ToolChoice* constants.
type ChatRequest struct {
	Messages       []Message
	Model          string
	MaxTokens      int
	SystemPrompt   string
	Tools          []Tool
	ToolChoice     string
	ResponseFormat *ResponseFormat
}

// ChatResponse holds the result of a completed chat request.
type ChatResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
	ToolCalls    []ToolCall
}

// LLMClient is the provider-agnostic AI chat interface.
type LLMClient interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// ProviderConfig is the plain, DB-free configuration used to construct an
// LLMClient. The gateway maps its models.AIProvider into this; the agent
// reconstructs it from the connection params shipped over the wire.
type ProviderConfig struct {
	Provider string // "openai" | "azure-openai" | "custom" | "anthropic"
	APIURL   *string
	APIKey   *string
	Model    string
}

// NewLLMClient returns an LLMClient for the given provider configuration.
//
// Provider routing:
//   - "openai", "azure-openai", "custom" → OpenAI-compatible client
//   - "anthropic"                         → Anthropic client
func NewLLMClient(cfg ProviderConfig) (LLMClient, error) {
	switch cfg.Provider {
	case "openai", "azure-openai", "custom":
		return newOpenAIClient(cfg)
	case "anthropic":
		return newAnthropicClient(cfg)
	default:
		return nil, fmt.Errorf("unsupported ai provider: %q", cfg.Provider)
	}
}

const defaultMaxTokens = 4096
