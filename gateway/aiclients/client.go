package aiclients

import (
	"context"
	"errors"
)

// ErrProviderNotConfigured is returned when no AI provider is configured for an org.
var ErrProviderNotConfigured = errors.New("ai provider not configured")

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

// --- Tools ---

// ToolProperty describes a single property within a tool's input JSON Schema.
type ToolProperty struct {
	Type        string // JSON Schema type: "string", "number", "integer", "boolean", "array", "object"
	Description string
	Enum        []string // optional allowed values
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
	Strict      bool // request strict schema adherence (supported by OpenAI and Anthropic)
}

// ToolCall is a tool invocation returned in the model response.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON-encoded arguments
}

// ToolChoice constants for ChatRequest.ToolChoice.
// An empty string leaves the choice to the provider default ("auto" when tools are present).
// Any value not matching a constant is treated as a specific tool name to force.
const (
	ToolChoiceAuto = "auto" // model decides whether to call a tool
	ToolChoiceAny  = "any"  // model must call at least one tool (Anthropic); mapped to "required" for OpenAI
	ToolChoiceNone = "none" // model must not call any tool
)

// --- Output format ---

// OutputFormatType controls how the model formats its output.
type OutputFormatType string

const (
	OutputFormatText       OutputFormatType = "text"        // plain text (default)
	OutputFormatJSONObject OutputFormatType = "json_object" // free-form valid JSON (no schema)
	OutputFormatJSONSchema OutputFormatType = "json_schema" // JSON conforming to a schema
)

// JSONSchemaDefinition holds the schema for structured output.
type JSONSchemaDefinition struct {
	// Name identifies the schema. Required by OpenAI; used as the internal tool name for Anthropic.
	Name        string
	Description string
	Schema      map[string]any // full JSON Schema object (e.g. {"type":"object","properties":{...}})
	Strict      bool           // enforce strict schema adherence
}

// ResponseFormat controls the output format of the model's response.
// When Type is OutputFormatJSONSchema, JSONSchema must be set.
// For Anthropic, OutputFormatJSONSchema is implemented via tool-forcing;
// the response Content will contain the schema-conforming JSON string.
type ResponseFormat struct {
	Type       OutputFormatType
	JSONSchema *JSONSchemaDefinition // required when Type == OutputFormatJSONSchema
}

// --- Request / Response ---

// ChatRequest holds the parameters for a chat completion request.
//
//   - Model overrides the provider's default model when non-empty.
//   - MaxTokens defaults to 4096 when zero.
//   - SystemPrompt is placed according to each provider's convention.
//   - ToolChoice is only applied when Tools is non-empty; see ToolChoiceAuto/Any/None constants.
type ChatRequest struct {
	Messages       []Message
	Model          string // overrides AIProvider.Model when set
	MaxTokens      int    // defaults to 4096 when zero
	SystemPrompt   string
	Tools          []Tool
	ToolChoice     string          // empty = provider default; see ToolChoice* constants
	ResponseFormat *ResponseFormat // nil = plain text
}

// ChatResponse holds the result of a completed chat request.
//
// When the model calls tools, ToolCalls is populated and Content may be empty.
// For OutputFormatJSONSchema, Content holds the JSON-encoded structured output.
type ChatResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
	ToolCalls    []ToolCall
}

// Client is the provider-agnostic AI chat interface.
type Client interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
