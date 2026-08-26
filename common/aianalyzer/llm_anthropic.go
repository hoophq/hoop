package aianalyzer

import (
	"context"
	"encoding/json"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// anthropicJSONOutputToolName is the synthetic tool name used to implement
// OutputFormatJSONSchema via tool-forcing on the Anthropic API.
const anthropicJSONOutputToolName = "__json_output__"

type anthropicClient struct {
	client anthropic.Client
	model  string
}

// newAnthropicClient constructs a client for the "anthropic" provider.
func newAnthropicClient(p ProviderConfig) (*anthropicClient, error) {
	if p.APIKey == nil {
		return nil, fmt.Errorf("anthropic: api_key is required")
	}
	opts := []option.RequestOption{option.WithAPIKey(*p.APIKey)}
	if p.APIURL != nil && *p.APIURL != "" {
		opts = append(opts, option.WithBaseURL(*p.APIURL))
	}
	return &anthropicClient{
		client: anthropic.NewClient(opts...),
		model:  p.Model,
	}, nil
}

// Chat sends a message to the Anthropic Messages API.
// The system prompt is placed in the top-level system field as Anthropic recommends.
// OutputFormatJSONSchema is implemented transparently via tool-forcing: a hidden tool
// is injected and the tool's input JSON is returned as ChatResponse.Content.
func (c *anthropicClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	messages := anthropicMessages(req.Messages)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: int64(maxTokens),
		Messages:  messages,
	}
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.SystemPrompt}}
	}

	// Build tool list and determine effective tool choice.
	allTools := anthropicTools(req.Tools)
	effectiveToolChoice := req.ToolChoice
	isJSONSchema := req.ResponseFormat != nil &&
		req.ResponseFormat.Type == OutputFormatJSONSchema &&
		req.ResponseFormat.JSONSchema != nil

	if isJSONSchema {
		// Inject a hidden tool representing the desired output schema and force the
		// model to call it. The tool's input will be the structured JSON output.
		allTools = append(allTools, anthropic.ToolUnionParam{
			OfTool: anthropicJSONSchemaTool(req.ResponseFormat.JSONSchema),
		})
		effectiveToolChoice = anthropicJSONOutputToolName
	}

	if len(allTools) > 0 {
		params.Tools = allTools
	}
	// Mirrors the documented ChatRequest contract (and the OpenAI client): a
	// tool choice is only meaningful alongside tools, and the API rejects one
	// sent without them.
	if effectiveToolChoice != "" && len(allTools) > 0 {
		params.ToolChoice = anthropicToolChoice(effectiveToolChoice)
	}

	msg, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic: message creation failed: %w", err)
	}
	if len(msg.Content) == 0 {
		return nil, fmt.Errorf("anthropic: empty response content")
	}

	var textContent string
	var toolCalls []ToolCall
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			textContent += block.AsText().Text
		case "tool_use":
			tu := block.AsToolUse()
			if tu.Name == anthropicJSONOutputToolName {
				// Transparent structured output: expose the tool input as Content.
				textContent = string(tu.Input)
			} else {
				toolCalls = append(toolCalls, ToolCall{
					ID:        tu.ID,
					Name:      tu.Name,
					Arguments: string(tu.Input),
				})
			}
		}
	}

	return &ChatResponse{
		Content:      textContent,
		Model:        string(msg.Model),
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
		ToolCalls:    toolCalls,
	}, nil
}

// anthropicMessages converts provider-agnostic turns to SDK message params.
//
// Consecutive tool results are folded into ONE user turn: that is the canonical
// shape for the Messages API, and emitting one message per result would rely on
// same-role merging, which strict-alternation intermediaries (Bedrock/Vertex
// proxies reachable via ProviderConfig.APIURL) may reject.
func anthropicMessages(in []Message) []anthropic.MessageParam {
	messages := make([]anthropic.MessageParam, 0, len(in))
	// lastWasToolResult tracks whether the open turn is a tool-result user turn
	// that further consecutive results may join.
	lastWasToolResult := false
	for _, m := range in {
		switch m.Role {
		case RoleUser:
			messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if m.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(m.Content))
				}
				for _, tc := range m.ToolCalls {
					var input any
					_ = json.Unmarshal([]byte(tc.Arguments), &input)
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
				}
				messages = append(messages, anthropic.NewAssistantMessage(blocks...))
			} else {
				messages = append(messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
			}
			// RoleSystem is not a valid turn role in the Anthropic API;
			// use ChatRequest.SystemPrompt for system instructions instead.
		case RoleTool:
			if m.ToolResult == nil {
				continue
			}
			block := anthropic.NewToolResultBlock(m.ToolResult.ToolCallID, m.ToolResult.Content, m.ToolResult.IsError)
			if n := len(messages); n > 0 && lastWasToolResult {
				messages[n-1].Content = append(messages[n-1].Content, block)
				continue
			}
			messages = append(messages, anthropic.NewUserMessage(block))
			lastWasToolResult = true
			continue
		}
		lastWasToolResult = false
	}
	return messages
}

// anthropicTools converts the provider-agnostic Tool slice to the SDK type.
func anthropicTools(tools []Tool) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		props := make(map[string]any, len(t.InputSchema.Properties))
		for name, prop := range t.InputSchema.Properties {
			p := map[string]any{"type": prop.Type}
			if prop.Description != "" {
				p["description"] = prop.Description
			}
			if len(prop.Enum) > 0 {
				p["enum"] = prop.Enum
			}
			props[name] = p
		}
		tp := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: props,
				Required:   t.InputSchema.Required,
			},
		}
		if t.Strict {
			tp.Strict = anthropic.Bool(true)
		}
		result[i] = anthropic.ToolUnionParam{OfTool: &tp}
	}
	return result
}

// anthropicToolChoice converts the provider-agnostic choice string to the SDK union type.
func anthropicToolChoice(choice string) anthropic.ToolChoiceUnionParam {
	switch choice {
	case ToolChoiceAuto:
		return anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
	case ToolChoiceAny:
		return anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
	case ToolChoiceNone:
		return anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
	default:
		// Treat as a specific tool name.
		return anthropic.ToolChoiceUnionParam{
			OfTool: &anthropic.ToolChoiceToolParam{Name: choice},
		}
	}
}

// anthropicJSONSchemaTool builds the hidden tool used to implement OutputFormatJSONSchema.
// The tool's InputSchema reflects the caller's JSONSchemaDefinition.Schema.
func anthropicJSONSchemaTool(def *JSONSchemaDefinition) *anthropic.ToolParam {
	inputSchema := anthropic.ToolInputSchemaParam{}
	if props, ok := def.Schema["properties"]; ok {
		inputSchema.Properties = props
	}
	// Support both []string and []any for the required field.
	switch req := def.Schema["required"].(type) {
	case []string:
		inputSchema.Required = req
	case []any:
		for _, r := range req {
			if s, ok := r.(string); ok {
				inputSchema.Required = append(inputSchema.Required, s)
			}
		}
	}

	tp := &anthropic.ToolParam{
		Name:        anthropicJSONOutputToolName,
		Description: anthropic.String(def.Description),
		InputSchema: inputSchema,
	}
	if def.Strict {
		tp.Strict = anthropic.Bool(true)
	}
	return tp
}
