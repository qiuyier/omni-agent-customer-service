package einoadapter

import (
	"context"
	"time"
)

// DefaultOpenAIModel balances response quality, latency, and cost for the
// customer-service default. Deployments can override it with AI_MODEL; the
// legacy OPENAI_MODEL variable remains supported by the console entry point.
const DefaultOpenAIModel = "gpt-5.6-terra"

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
