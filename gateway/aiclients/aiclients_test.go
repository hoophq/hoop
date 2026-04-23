package aiclients_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hoophq/hoop/gateway/aiclients"
	"github.com/hoophq/hoop/gateway/models"
)

func strPtr(s string) *string { return &s }

func TestNewClient_UnsupportedProvider(t *testing.T) {
	p := models.AIProvider{Provider: "huggingface"}
	_, err := aiclients.NewClient(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported ai provider")
}

// -- OpenAI / Azure / Custom provider construction --

func TestNewClient_OpenAIMissingAPIKey(t *testing.T) {
	p := models.AIProvider{Provider: "openai"}
	_, err := aiclients.NewClient(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key is required")
}

func TestNewClient_OpenAIReturnsClient(t *testing.T) {
	p := models.AIProvider{
		Provider: "openai",
		ApiKey:   strPtr("sk-test"),
		Model:    "gpt-4o",
	}
	client, err := aiclients.NewClient(p)
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewClient_AzureOpenAIReturnsClient(t *testing.T) {
	p := models.AIProvider{
		Provider: "azure-openai",
		ApiKey:   strPtr("az-key"),
		ApiUrl:   strPtr("https://my-instance.openai.azure.com"),
		Model:    "gpt-4o",
	}
	client, err := aiclients.NewClient(p)
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestNewClient_CustomProviderReturnsClient(t *testing.T) {
	p := models.AIProvider{
		Provider: "custom",
		ApiKey:   strPtr("custom-key"),
		ApiUrl:   strPtr("https://my-api.example.com"),
		Model:    "my-model",
	}
	client, err := aiclients.NewClient(p)
	require.NoError(t, err)
	assert.NotNil(t, client)
}

// -- Anthropic provider construction --

func TestNewClient_AnthropicMissingAPIKey(t *testing.T) {
	p := models.AIProvider{Provider: "anthropic"}
	_, err := aiclients.NewClient(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key is required")
}

func TestNewClient_AnthropicReturnsClient(t *testing.T) {
	p := models.AIProvider{
		Provider: "anthropic",
		ApiKey:   strPtr("ant-key"),
		Model:    "claude-sonnet-4-6",
	}
	client, err := aiclients.NewClient(p)
	require.NoError(t, err)
	assert.NotNil(t, client)
}
