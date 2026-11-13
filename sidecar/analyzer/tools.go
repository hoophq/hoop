package analyzer

import (
	"context"
	"encoding/json"
)

// ToolInput is a tool's argument object, as the JSON Schema subset every
// provider accepts: named properties, and the ones the model must fill.
type ToolInput struct {
	Properties map[string]ToolProperty
	Required   []string
}

// ToolProperty is one argument. Type is a JSON Schema type ("string",
// "integer", "number", "boolean", "array"); Enum, when set, lists the only
// values the model may send. Items describes the elements of an "array",
// and must be set for one.
type ToolProperty struct {
	Type        string
	Description string
	Enum        []string
	Items       *ToolProperty
}

// ToolCall is one tool the model called. Args is the argument object as the
// provider sent it, or nil when the provider sent something that is not JSON.
type ToolCall struct {
	Name string
	Args json.RawMessage
}

// ToolCaller is a Provider that can send a caller-supplied tool set and
// report every call the model made. The approval agent uses it; the risk
// classification does not, so a provider without it still classifies.
//
// The caller judges the calls: none, several, or an unknown name come back
// as they are. A refusal or a provider failure is an error.
type ToolCaller interface {
	Decide(ctx context.Context, systemPrompt, content string, tools []ToolSpec) ([]ToolCall, error)
}
