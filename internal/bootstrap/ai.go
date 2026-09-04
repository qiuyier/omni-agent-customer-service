// Package bootstrap contains composition helpers shared by executable entry
// points. It may depend on concrete adapters; application and domain packages
// must not depend on it.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const (
	// DefaultAIReplyTimeout bounds the complete model-generation attempt. The
	// same value is also applied to individual provider requests.
	DefaultAIReplyTimeout = 75 * time.Second

	// DefaultAIMaxConcurrentReplies bounds simultaneous model generations in
	// one server process. Streaming replies hold a slot until their model stream
	// finishes or is closed.
	DefaultAIMaxConcurrentReplies = 8

	// DefaultAISystemPrompt is shared by every executable that builds the
	// customer-service reply generator.
	DefaultAISystemPrompt = `你是企业智能客服。请使用与客户相同的语言回答，表达清晰、友好、简洁。
只根据对话中已知的信息作答；信息不足时提出一个明确的澄清问题，不要编造订单、政策或处理结果。`
)

var ErrInvalidAIConfiguration = errors.New("bootstrap: invalid AI configuration")

// AIReplyGenerator is the complete model capability used by the HTTP server.
// Keeping both ports here lets the application layer remain independent from
// Eino and from any concrete model provider.
type AIReplyGenerator interface {
	customerservice.ReplyGenerator
	customerservice.ReplyStreamGenerator
}

// LookupEnv abstracts the process environment so configuration tests do not
// mutate global state.
type LookupEnv func(key string) (value string, found bool)

// AIOverrides contains non-secret command-line overrides. API keys are never
// accepted here because command arguments can be exposed by process listings.
type AIOverrides struct {
	Provider string
	Protocol string
	Model    string
	BaseURL  string
}

// AIReplyRuntimeConfig contains application-level model execution policy. It
// is deliberately separate from provider credentials/protocol configuration
// and from HTTP-boundary API configuration.
type AIReplyRuntimeConfig struct {
	MaxConcurrentReplies int
}

// ResolveAIReplyRuntimeConfig loads process-local reply execution limits.
func ResolveAIReplyRuntimeConfig(lookup LookupEnv) (AIReplyRuntimeConfig, error) {
	if lookup == nil {
		return AIReplyRuntimeConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidAIConfiguration,
		)
	}

	value := strings.TrimSpace(environmentValue(lookup, "AI_MAX_CONCURRENT_REPLIES"))
	if value == "" {
		return AIReplyRuntimeConfig{
			MaxConcurrentReplies: DefaultAIMaxConcurrentReplies,
		}, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return AIReplyRuntimeConfig{}, fmt.Errorf(
			"%w: AI_MAX_CONCURRENT_REPLIES must be a positive integer",
			ErrInvalidAIConfiguration,
		)
	}
	return AIReplyRuntimeConfig{MaxConcurrentReplies: parsed}, nil
}

// ResolveAIConfig combines command-line overrides, generic AI_* variables and
// provider-specific compatibility variables into one adapter configuration.
func ResolveAIConfig(
	lookup LookupEnv,
	overrides AIOverrides,
) (einoadapter.ProviderConfig, error) {
	if lookup == nil {
		return einoadapter.ProviderConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidAIConfiguration,
		)
	}

	provider, err := einoadapter.ParseProvider(firstNonBlank(
		overrides.Provider,
		environmentValue(lookup, "AI_PROVIDER"),
		string(einoadapter.ProviderOpenAI),
	))
	if err != nil {
		return einoadapter.ProviderConfig{}, fmt.Errorf("resolve AI provider: %w", err)
	}

	var protocol einoadapter.Protocol
	protocolName := firstNonBlank(overrides.Protocol, environmentValue(lookup, "AI_PROTOCOL"))
	if protocolName != "" {
		protocol, err = einoadapter.ParseProtocol(protocolName)
		if err != nil {
			return einoadapter.ProviderConfig{}, fmt.Errorf("resolve AI protocol: %w", err)
		}
	}

	modelName := firstNonBlank(overrides.Model, environmentValue(lookup, "AI_MODEL"))
	baseURL := firstNonBlank(overrides.BaseURL, environmentValue(lookup, "AI_BASE_URL"))
	if provider == einoadapter.ProviderOpenAI {
		modelName = firstNonBlank(modelName, environmentValue(lookup, "OPENAI_MODEL"))
		baseURL = firstNonBlank(baseURL, environmentValue(lookup, "OPENAI_BASE_URL"))
	}
	if modelName == "" && provider != einoadapter.ProviderOpenAI {
		return einoadapter.ProviderConfig{}, fmt.Errorf(
			"%w: AI_MODEL must be set for provider %s",
			ErrInvalidAIConfiguration,
			provider,
		)
	}
	if baseURL == "" && provider == einoadapter.ProviderCustom {
		return einoadapter.ProviderConfig{}, fmt.Errorf(
			"%w: AI_BASE_URL must be set for provider custom",
			ErrInvalidAIConfiguration,
		)
	}

	apiKeyEnvironment, err := einoadapter.ProviderAPIKeyEnvironmentVariable(provider)
	if err != nil {
		return einoadapter.ProviderConfig{}, fmt.Errorf("resolve AI credential source: %w", err)
	}
	apiKey := firstNonBlank(
		environmentValue(lookup, "AI_API_KEY"),
		environmentValue(lookup, apiKeyEnvironment),
	)
	if apiKey == "" && provider != einoadapter.ProviderCustom {
		return einoadapter.ProviderConfig{}, fmt.Errorf(
			"%w: AI_API_KEY or %s must be set",
			ErrInvalidAIConfiguration,
			apiKeyEnvironment,
		)
	}

	systemPrompt := DefaultAISystemPrompt
	if configured, found := lookup("AI_SYSTEM_PROMPT"); found && strings.TrimSpace(configured) != "" {
		systemPrompt = configured
	}
	allowInsecureHTTP, err := environmentBool(lookup, "AI_ALLOW_INSECURE_HTTP")
	if err != nil {
		return einoadapter.ProviderConfig{}, err
	}

	return einoadapter.ProviderConfig{
		Provider:          provider,
		Protocol:          protocol,
		APIKey:            apiKey,
		Model:             modelName,
		BaseURL:           baseURL,
		Timeout:           DefaultAIReplyTimeout,
		SystemPrompt:      systemPrompt,
		Headers:           providerHeaders(lookup, provider),
		AllowInsecureHTTP: allowInsecureHTTP,
	}, nil
}

// NewAIReplyGenerator validates the resolved adapter configuration and creates
// the provider-independent application port.
func NewAIReplyGenerator(
	ctx context.Context,
	config einoadapter.ProviderConfig,
) (AIReplyGenerator, error) {
	generator, err := einoadapter.NewProviderGenerator(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create AI reply generator: %w", err)
	}
	return generator, nil
}

func environmentValue(lookup LookupEnv, name string) string {
	if name == "" {
		return ""
	}
	value, _ := lookup(name)
	return value
}

func environmentBool(lookup LookupEnv, name string) (bool, error) {
	value := strings.TrimSpace(environmentValue(lookup, name))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf(
			"%w: %s must be true or false",
			ErrInvalidAIConfiguration,
			name,
		)
	}
	return parsed, nil
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func providerHeaders(lookup LookupEnv, provider einoadapter.Provider) map[string]string {
	if provider != einoadapter.ProviderOpenRouter {
		return nil
	}
	headers := make(map[string]string, 2)
	if value := firstNonBlank(environmentValue(lookup, "OPENROUTER_SITE_URL")); value != "" {
		headers["HTTP-Referer"] = value
	}
	if value := firstNonBlank(environmentValue(lookup, "OPENROUTER_APP_NAME")); value != "" {
		headers["X-OpenRouter-Title"] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}
