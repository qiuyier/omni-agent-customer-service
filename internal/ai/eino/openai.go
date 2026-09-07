package einoadapter

import (
	"context"
	"time"
)

// DefaultOpenAIModel is the OpenAI model used when no deployment override is
// supplied. AI_MODEL and the legacy OPENAI_MODEL can select another model.
const DefaultOpenAIModel = "gpt-6-astra"

// OpenAIResponsesConfig contains the composition-time settings for OpenAI's
// Responses API. APIKey must come from a secret source such as an environment
// variable; it must never be logged or persisted with application data.
type OpenAIResponsesConfig struct {
	APIKey       string
	Model        string
	BaseURL      string
	Timeout      time.Duration
	SystemPrompt string
}

// NewOpenAIResponsesGenerator creates the concrete OpenAI model and hides it
// behind customerservice.ReplyGenerator. Constructing it does not make an API
// request.
func NewOpenAIResponsesGenerator(
	ctx context.Context,
	config OpenAIResponsesConfig,
) (*AgenticGenerator, error) {
	return NewProviderGenerator(ctx, ProviderConfig{
		Provider:     ProviderOpenAI,
		Protocol:     ProtocolResponses,
		APIKey:       config.APIKey,
		Model:        config.Model,
		BaseURL:      config.BaseURL,
		Timeout:      config.Timeout,
		SystemPrompt: config.SystemPrompt,
	})
}
