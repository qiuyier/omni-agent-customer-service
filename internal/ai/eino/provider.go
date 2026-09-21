package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
)

// Provider selects a platform preset. It does not leak into the application
// layer and does not imply that every platform owns a different adapter.
type Provider string

const (
	ProviderOpenAI      Provider = "openai"
	ProviderDeepSeek    Provider = "deepseek"
	ProviderGLM         Provider = "glm"
	ProviderKimi        Provider = "kimi"
	ProviderQwen        Provider = "qwen"
	ProviderOpenRouter  Provider = "openrouter"
	ProviderSiliconFlow Provider = "siliconflow"
	ProviderCustom      Provider = "custom"
)

// Protocol identifies the HTTP generation API spoken by a provider endpoint.
// Provider and protocol are separate because one platform can support more
// than one protocol, sometimes only for selected models.
type Protocol string

const (
	ProtocolResponses       Protocol = "responses"
	ProtocolChatCompletions Protocol = "chat_completions"
)

var (
	// ErrUnknownProvider indicates an unsupported provider preset name.
	ErrUnknownProvider = errors.New("eino reply adapter: unknown provider")

	// ErrUnsupportedProtocol indicates an unsupported generation protocol.
	ErrUnsupportedProtocol = errors.New("eino reply adapter: unsupported protocol")
)

// ProviderConfig configures one Eino model through a platform preset or a
// fully custom OpenAI-compatible endpoint. Empty Protocol and BaseURL fields
// inherit the provider preset. Model only defaults for OpenAI. ExtraFields
// must not contain secrets, and nested map or slice values must be treated as
// immutable after construction. AllowInsecureHTTP is a dangerous explicit
// opt-in for non-loopback endpoints because prompts and conversations are sent
// in plaintext.
type ProviderConfig struct {
	Provider          Provider
	Protocol          Protocol
	APIKey            string
	Model             string
	BaseURL           string
	Timeout           time.Duration
	SystemPrompt      string
	Headers           map[string]string
	ExtraFields       map[string]any
	AllowInsecureHTTP bool
}

type providerPreset struct {
	protocol     Protocol
	baseURL      string
	defaultModel string
	apiKeyEnv    string
}

var providerPresets = map[Provider]providerPreset{
	ProviderOpenAI: {
		protocol:     ProtocolResponses,
		baseURL:      "https://api.openai.com/v1",
		defaultModel: DefaultOpenAIModel,
		apiKeyEnv:    "OPENAI_API_KEY",
	},
	ProviderDeepSeek: {
		protocol:  ProtocolChatCompletions,
		baseURL:   "https://api.deepseek.com",
		apiKeyEnv: "DEEPSEEK_API_KEY",
	},
	ProviderGLM: {
		protocol:  ProtocolChatCompletions,
		baseURL:   "https://open.bigmodel.cn/api/paas/v4",
		apiKeyEnv: "ZHIPUAI_API_KEY",
	},
	ProviderKimi: {
		protocol:  ProtocolChatCompletions,
		baseURL:   "https://api.moonshot.cn/v1",
		apiKeyEnv: "MOONSHOT_API_KEY",
	},
	ProviderQwen: {
		protocol:  ProtocolChatCompletions,
		baseURL:   "https://dashscope.aliyuncs.com/compatible-mode/v1",
		apiKeyEnv: "DASHSCOPE_API_KEY",
	},
	ProviderOpenRouter: {
		protocol:  ProtocolChatCompletions,
		baseURL:   "https://openrouter.ai/api/v1",
		apiKeyEnv: "OPENROUTER_API_KEY",
	},
	ProviderSiliconFlow: {
		protocol:  ProtocolChatCompletions,
		baseURL:   "https://api.siliconflow.cn/v1",
		apiKeyEnv: "SILICONFLOW_API_KEY",
	},
	ProviderCustom: {
		protocol: ProtocolChatCompletions,
	},
}

// ParseProvider normalizes a provider name and accepts common platform aliases.
func ParseProvider(value string) (Provider, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", string(ProviderOpenAI):
		return ProviderOpenAI, nil
	case string(ProviderDeepSeek):
		return ProviderDeepSeek, nil
	case string(ProviderGLM), "zhipu", "bigmodel":
		return ProviderGLM, nil
	case string(ProviderKimi), "moonshot":
		return ProviderKimi, nil
	case string(ProviderQwen), "dashscope", "bailian":
		return ProviderQwen, nil
	case string(ProviderOpenRouter), "open_router", "open-router":
		return ProviderOpenRouter, nil
	case string(ProviderSiliconFlow), "silicon_flow", "silicon-flow":
		return ProviderSiliconFlow, nil
	case string(ProviderCustom):
		return ProviderCustom, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownProvider, value)
	}
}

// ParseProtocol normalizes supported protocol aliases.
func ParseProtocol(value string) (Protocol, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case string(ProtocolResponses), "response":
		return ProtocolResponses, nil
	case string(ProtocolChatCompletions), "chat", "chat-completions", "openai_compatible", "openai-compatible":
		return ProtocolChatCompletions, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedProtocol, value)
	}
}

// ProviderAPIKeyEnvironmentVariable returns the conventional provider-specific
// environment variable used when the generic AI_API_KEY is not set.
func ProviderAPIKeyEnvironmentVariable(provider Provider) (string, error) {
	canonical, err := ParseProvider(string(provider))
	if err != nil {
		return "", err
	}
	return providerPresets[canonical].apiKeyEnv, nil
}

// ValidateProviderConfig checks the same configuration rules as the generator
// constructor without creating a model or making any network requests. Return
// only a stable error: provider names, URLs and extra fields may hold secrets.
func ValidateProviderConfig(config ProviderConfig) error {
	if _, err := resolveProviderConfig(context.Background(), config); err != nil {
		return ErrInvalidConfiguration
	}
	return nil
}

// NewProviderGenerator builds a single ReplyGenerator for OpenAI Responses or
// any OpenAI-compatible Chat Completions endpoint. Adding another compatible
// platform normally requires configuration or one preset entry, not a new
// application adapter.
func NewProviderGenerator(
	ctx context.Context,
	config ProviderConfig,
) (*AgenticGenerator, error) {
	resolved, err := resolveProviderConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	var agenticModel AgenticModel
	switch resolved.Protocol {
	case ProtocolResponses:
		store := false
		responsesConfig := &agenticopenai.ResponsesConfig{
			APIKey:        resolved.APIKey,
			Model:         resolved.Model,
			BaseURL:       resolved.BaseURL,
			Store:         &store,
			CustomHeaders: cloneStringMap(resolved.Headers),
		}
		if resolved.Timeout > 0 {
			timeout := resolved.Timeout
			responsesConfig.Timeout = &timeout
		}
		agenticModel, err = agenticopenai.NewResponsesModel(ctx, responsesConfig)
	case ProtocolChatCompletions:
		agenticModel, err = agenticopenai.NewChatModel(ctx, &agenticopenai.ChatConfig{
			APIKey:        resolved.APIKey,
			Model:         resolved.Model,
			BaseURL:       resolved.BaseURL,
			Timeout:       resolved.Timeout,
			CustomHeaders: cloneStringMap(resolved.Headers),
			ExtraFields:   cloneAnyMap(resolved.ExtraFields),
		})
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, resolved.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf(
			"create %s model for provider %s: %w",
			resolved.Protocol,
			resolved.Provider,
			err,
		)
	}
	return NewAgenticGenerator(agenticModel, resolved.SystemPrompt)
}

func resolveProviderConfig(ctx context.Context, config ProviderConfig) (ProviderConfig, error) {
	if ctx == nil {
		return ProviderConfig{}, fmt.Errorf("%w: context is nil", ErrInvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return ProviderConfig{}, err
	}

	provider, err := ParseProvider(string(config.Provider))
	if err != nil {
		return ProviderConfig{}, err
	}
	preset := providerPresets[provider]
	protocol := preset.protocol
	if strings.TrimSpace(string(config.Protocol)) != "" {
		protocol, err = ParseProtocol(string(config.Protocol))
		if err != nil {
			return ProviderConfig{}, err
		}
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" && provider != ProviderCustom {
		return ProviderConfig{}, fmt.Errorf("%w: API key is required", ErrInvalidConfiguration)
	}
	modelName := strings.TrimSpace(config.Model)
	if modelName == "" {
		modelName = preset.defaultModel
	}
	if modelName == "" {
		return ProviderConfig{}, fmt.Errorf(
			"%w: model is required for provider %s",
			ErrInvalidConfiguration,
			provider,
		)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = preset.baseURL
	}
	if err := validateProviderBaseURL(baseURL, config.AllowInsecureHTTP); err != nil {
		return ProviderConfig{}, err
	}
	if config.Timeout < 0 {
		return ProviderConfig{}, fmt.Errorf("%w: timeout must not be negative", ErrInvalidConfiguration)
	}
	for name := range config.Headers {
		if strings.TrimSpace(name) == "" {
			return ProviderConfig{}, fmt.Errorf(
				"%w: custom header name must not be blank",
				ErrInvalidConfiguration,
			)
		}
		if strings.EqualFold(strings.TrimSpace(name), "authorization") &&
			(provider != ProviderCustom || apiKey != "") {
			return ProviderConfig{}, fmt.Errorf(
				"%w: Authorization header conflicts with APIKey authentication",
				ErrInvalidConfiguration,
			)
		}
	}
	if err := validateExtraFields(protocol, config.ExtraFields); err != nil {
		return ProviderConfig{}, err
	}

	return ProviderConfig{
		Provider:          provider,
		Protocol:          protocol,
		APIKey:            apiKey,
		Model:             modelName,
		BaseURL:           baseURL,
		Timeout:           config.Timeout,
		SystemPrompt:      config.SystemPrompt,
		Headers:           cloneStringMap(config.Headers),
		ExtraFields:       cloneAnyMap(config.ExtraFields),
		AllowInsecureHTTP: config.AllowInsecureHTTP,
	}, nil
}

func validateProviderBaseURL(value string, allowInsecureHTTP bool) error {
	if value == "" {
		return fmt.Errorf("%w: base URL is required", ErrInvalidConfiguration)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: base URL must be an absolute HTTP or HTTPS URL", ErrInvalidConfiguration)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: base URL must not contain user information", ErrInvalidConfiguration)
	}
	if parsed.Scheme != "https" && !isLoopbackHost(parsed.Hostname()) && !allowInsecureHTTP {
		return fmt.Errorf(
			"%w: base URL must use HTTPS outside loopback unless insecure HTTP is explicitly allowed",
			ErrInvalidConfiguration,
		)
	}
	if parsed.ForceQuery || parsed.RawQuery != "" || strings.Contains(value, "#") {
		return fmt.Errorf("%w: base URL must not contain a query or fragment", ErrInvalidConfiguration)
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/responses") {
		return fmt.Errorf(
			"%w: base URL must be the API root; the adapter appends the protocol path",
			ErrInvalidConfiguration,
		)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateExtraFields(protocol Protocol, fields map[string]any) error {
	if protocol == ProtocolResponses && len(fields) > 0 {
		return fmt.Errorf(
			"%w: extra fields are not supported for Responses by the current Eino version",
			ErrInvalidConfiguration,
		)
	}
	reserved := map[string]struct{}{"model": {}, "stream": {}}
	switch protocol {
	case ProtocolResponses:
		reserved["input"] = struct{}{}
		reserved["instructions"] = struct{}{}
		reserved["store"] = struct{}{}
	case ProtocolChatCompletions:
		reserved["messages"] = struct{}{}
	}
	for name := range fields {
		normalized := strings.ToLower(strings.TrimSpace(name))
		if normalized == "" {
			return fmt.Errorf("%w: extra field name must not be blank", ErrInvalidConfiguration)
		}
		if _, found := reserved[normalized]; found {
			return fmt.Errorf(
				"%w: extra field %q is managed by the adapter",
				ErrInvalidConfiguration,
				name,
			)
		}
	}
	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
