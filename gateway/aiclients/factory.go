package aiclients

import (
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
)

// NewClient returns a Client for the given AI provider configuration.
//
// Provider routing:
//   - "openai", "azure-openai", "custom" → OpenAI-compatible client
//   - "anthropic"                         → Anthropic client
//
// Returns [ErrProviderNotConfigured] when provider is nil or has no Provider field set.
func NewClient(provider models.AIProvider) (Client, error) {
	switch provider.Provider {
	case "openai", "azure-openai", "custom":
		return newOpenAIClient(&provider)
	case "anthropic":
		return newAnthropicClient(&provider)
	default:
		return nil, fmt.Errorf("unsupported ai provider: %q", provider.Provider)
	}
}
