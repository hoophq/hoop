package aiclients

import (
	"context"
	"fmt"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/hoophq/hoop/gateway/models"
)

const defaultMaxTokens = 4096

type openaiClient struct {
	client openai.Client
	model  string
}

// newOpenAIClient constructs a client for "openai", "azure-openai", and "custom" providers.
// When ApiUrl is set it overrides the default OpenAI base URL, which is required for
// Azure OpenAI deployments and self-hosted / compatible endpoints.
func newOpenAIClient(p *models.AIProvider) (*openaiClient, error) {
	if p.ApiKey == nil {
		return nil, fmt.Errorf("openai: api_key is required")
	}
	opts := []option.RequestOption{option.WithAPIKey(*p.ApiKey)}
	if p.ApiUrl != nil && *p.ApiUrl != "" {
		opts = append(opts, option.WithBaseURL(*p.ApiUrl))
	}
	return &openaiClient{
		client: openai.NewClient(opts...),
		model:  p.Model,
	}, nil
}

// Chat sends a chat completion request to the OpenAI-compatible API.
func (c *openaiClient) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, openai.SystemMessage(req.SystemPrompt))
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			messages = append(messages, openai.UserMessage(m.Content))
		case RoleAssistant:
			messages = append(messages, openai.AssistantMessage(m.Content))
		case RoleSystem:
			messages = append(messages, openai.SystemMessage(m.Content))
		}
	}

	params := openai.ChatCompletionNewParams{
		Model:               model,
		Messages:            messages,
		MaxCompletionTokens: openai.Int(int64(maxTokens)),
	}
	if len(req.Tools) > 0 {
		params.Tools = openaiTools(req.Tools)
		if req.ToolChoice != "" {
			params.ToolChoice = openaiToolChoice(req.ToolChoice)
		}
	}
	if req.ResponseFormat != nil {
		params.ResponseFormat = openaiResponseFormat(req.ResponseFormat)
	}

	completion, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai: chat completion failed: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("openai: no choices returned in response")
	}

	choice := completion.Choices[0]
	resp := &ChatResponse{
		Content:      choice.Message.Content,
		Model:        completion.Model,
		InputTokens:  int(completion.Usage.PromptTokens),
		OutputTokens: int(completion.Usage.CompletionTokens),
	}
	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return resp, nil
}

// openaiTools converts the provider-agnostic Tool slice to the SDK type.
func openaiTools(tools []Tool) []openai.ChatCompletionToolParam {
	result := make([]openai.ChatCompletionToolParam, len(tools))
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
		params := openai.FunctionParameters{"type": "object", "properties": props}
		if len(t.InputSchema.Required) > 0 {
			params["required"] = t.InputSchema.Required
		}
		result[i] = openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Strict:      openai.Bool(t.Strict),
				Parameters:  params,
			},
		}
	}
	return result
}

// openaiToolChoice converts the provider-agnostic choice string to the SDK union type.
func openaiToolChoice(choice string) openai.ChatCompletionToolChoiceOptionUnionParam {
	switch choice {
	case ToolChoiceNone:
		return openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("none")}
	case ToolChoiceAny:
		// Anthropic "any" = OpenAI "required": model must call at least one tool.
		return openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("required")}
	case ToolChoiceAuto:
		return openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")}
	default:
		// Treat as a specific tool name.
		return openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(
			openai.ChatCompletionNamedToolChoiceFunctionParam{Name: choice},
		)
	}
}

// openaiResponseFormat converts the provider-agnostic ResponseFormat to the SDK union type.
// For OutputFormatText or nil, the field is left zero (default plaintext).
func openaiResponseFormat(rf *ResponseFormat) openai.ChatCompletionNewParamsResponseFormatUnion {
	switch rf.Type {
	case OutputFormatJSONObject:
		return openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		}
	case OutputFormatJSONSchema:
		if rf.JSONSchema == nil {
			break
		}
		return openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:        rf.JSONSchema.Name,
					Description: openai.String(rf.JSONSchema.Description),
					Schema:      rf.JSONSchema.Schema,
					Strict:      openai.Bool(rf.JSONSchema.Strict),
				},
			},
		}
	}
	// OutputFormatText or unrecognised: return zero value (no ResponseFormat sent).
	return openai.ChatCompletionNewParamsResponseFormatUnion{}
}
