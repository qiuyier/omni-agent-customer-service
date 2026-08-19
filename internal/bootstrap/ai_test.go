package bootstrap_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func TestResolveAIConfigUsesDefaultsAndProviderCredential(t *testing.T) {
	config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
		"OPENAI_API_KEY": "openai-test-key",
	}), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatalf("ResolveAIConfig() error = %v", err)
	}
	if config.Provider != einoadapter.ProviderOpenAI {
		t.Fatalf("provider = %q, want %q", config.Provider, einoadapter.ProviderOpenAI)
	}
	if config.Protocol != "" || config.Model != "" || config.BaseURL != "" {
		t.Fatalf("preset fields should remain unresolved: %#v", config)
	}
	if config.APIKey != "openai-test-key" {
		t.Fatalf("API key = %q", config.APIKey)
	}
	if config.Timeout != bootstrap.DefaultAIReplyTimeout {
		t.Fatalf("timeout = %v, want %v", config.Timeout, bootstrap.DefaultAIReplyTimeout)
	}
	if config.SystemPrompt != bootstrap.DefaultAISystemPrompt {
		t.Fatalf("system prompt = %q", config.SystemPrompt)
	}
}

func TestResolveAIConfigAppliesOverridePrecedence(t *testing.T) {
	config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
		"AI_PROVIDER":     "openai",
		"AI_PROTOCOL":     "responses",
		"AI_API_KEY":      "generic-key",
		"AI_MODEL":        "environment-model",
		"AI_BASE_URL":     "https://environment.example/v1",
		"OPENAI_API_KEY":  "provider-key",
		"OPENAI_MODEL":    "legacy-model",
		"OPENAI_BASE_URL": "https://legacy.example/v1",
	}), bootstrap.AIOverrides{
		Provider: "deepseek",
		Protocol: "chat",
		Model:    "override-model",
		BaseURL:  "https://override.example/v1",
	})
	if err != nil {
		t.Fatalf("ResolveAIConfig() error = %v", err)
	}
	if config.Provider != einoadapter.ProviderDeepSeek ||
		config.Protocol != einoadapter.ProtocolChatCompletions ||
		config.APIKey != "generic-key" ||
		config.Model != "override-model" ||
		config.BaseURL != "https://override.example/v1" {
		t.Fatalf("resolved config = %#v", config)
	}
}

func TestResolveAIConfigSupportsProviderSpecificFallbacks(t *testing.T) {
	t.Run("non OpenAI provider key", func(t *testing.T) {
		config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
			"AI_PROVIDER":      "deepseek",
			"AI_MODEL":         "deepseek-model",
			"DEEPSEEK_API_KEY": "deepseek-key",
		}), bootstrap.AIOverrides{})
		if err != nil {
			t.Fatalf("ResolveAIConfig() error = %v", err)
		}
		if config.APIKey != "deepseek-key" || config.Model != "deepseek-model" {
			t.Fatalf("resolved config = %#v", config)
		}
	})

	t.Run("OpenAI legacy model and endpoint", func(t *testing.T) {
		config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
			"OPENAI_API_KEY":  "openai-key",
			"OPENAI_MODEL":    "legacy-model",
			"OPENAI_BASE_URL": "https://legacy.example/v1",
		}), bootstrap.AIOverrides{})
		if err != nil {
			t.Fatalf("ResolveAIConfig() error = %v", err)
		}
		if config.Model != "legacy-model" || config.BaseURL != "https://legacy.example/v1" {
			t.Fatalf("resolved config = %#v", config)
		}
	})

	t.Run("custom endpoint without key", func(t *testing.T) {
		config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
			"AI_PROVIDER": "custom",
			"AI_MODEL":    "local-model",
			"AI_BASE_URL": "http://127.0.0.1:11434/v1",
		}), bootstrap.AIOverrides{})
		if err != nil {
			t.Fatalf("ResolveAIConfig() error = %v", err)
		}
		if config.APIKey != "" || config.Provider != einoadapter.ProviderCustom {
			t.Fatalf("resolved config = %#v", config)
		}
	})
}

func TestResolveAIConfigScopesOpenRouterHeaders(t *testing.T) {
	values := map[string]string{
		"AI_PROVIDER":         "openrouter",
		"AI_MODEL":            "provider/model",
		"OPENROUTER_API_KEY":  "openrouter-key",
		"OPENROUTER_SITE_URL": " https://support.example ",
		"OPENROUTER_APP_NAME": " Support Tool ",
		"OPENAI_API_KEY":      "must-not-win",
		"OPENAI_MODEL":        "must-not-win",
		"OPENAI_BASE_URL":     "https://must-not-win.example/v1",
	}
	config, err := bootstrap.ResolveAIConfig(environment(values), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatalf("ResolveAIConfig() error = %v", err)
	}
	if config.Headers["HTTP-Referer"] != "https://support.example" ||
		config.Headers["X-OpenRouter-Title"] != "Support Tool" {
		t.Fatalf("headers = %#v", config.Headers)
	}

	config.Headers["X-Modified"] = "first call only"
	second, err := bootstrap.ResolveAIConfig(environment(values), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if _, found := second.Headers["X-Modified"]; found {
		t.Fatalf("headers map was reused: %#v", second.Headers)
	}

	nonOpenRouter, err := bootstrap.ResolveAIConfig(environment(map[string]string{
		"AI_PROVIDER":         "deepseek",
		"AI_MODEL":            "deepseek-model",
		"DEEPSEEK_API_KEY":    "deepseek-key",
		"OPENROUTER_SITE_URL": "https://must-not-send.example",
		"OPENROUTER_APP_NAME": "must-not-send",
	}), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if nonOpenRouter.Headers != nil {
		t.Fatalf("non OpenRouter headers = %#v, want nil", nonOpenRouter.Headers)
	}
}

func TestResolveAIConfigSupportsSystemPromptOverride(t *testing.T) {
	const prompt = "  answer only from policy  "
	config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
		"OPENAI_API_KEY":   "openai-key",
		"AI_SYSTEM_PROMPT": prompt,
	}), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if config.SystemPrompt != prompt {
		t.Fatalf("system prompt = %q, want exact configured value %q", config.SystemPrompt, prompt)
	}
}

func TestResolveAIConfigRequiresExplicitInsecureHTTPOptIn(t *testing.T) {
	config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
		"AI_PROVIDER":            "custom",
		"AI_MODEL":               "internal-model",
		"AI_BASE_URL":            "http://models.internal/v1",
		"AI_ALLOW_INSECURE_HTTP": "true",
	}), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !config.AllowInsecureHTTP {
		t.Fatal("AllowInsecureHTTP = false, want true")
	}
}

func TestResolveAIConfigRejectsInvalidConfigurationWithoutLeakingSecrets(t *testing.T) {
	tests := []struct {
		name      string
		lookup    bootstrap.LookupEnv
		overrides bootstrap.AIOverrides
		wantErr   error
	}{
		{
			name:    "nil environment lookup",
			wantErr: bootstrap.ErrInvalidAIConfiguration,
		},
		{
			name: "unknown provider",
			lookup: environment(map[string]string{
				"AI_PROVIDER": "unknown",
			}),
			wantErr: einoadapter.ErrUnknownProvider,
		},
		{
			name: "unsupported protocol",
			lookup: environment(map[string]string{
				"OPENAI_API_KEY": "openai-key",
				"AI_PROTOCOL":    "legacy-completions",
			}),
			wantErr: einoadapter.ErrUnsupportedProtocol,
		},
		{
			name:    "missing provider key",
			lookup:  environment(map[string]string{}),
			wantErr: bootstrap.ErrInvalidAIConfiguration,
		},
		{
			name: "missing non OpenAI model",
			lookup: environment(map[string]string{
				"AI_PROVIDER":      "deepseek",
				"DEEPSEEK_API_KEY": "secret-that-must-not-leak",
			}),
			wantErr: bootstrap.ErrInvalidAIConfiguration,
		},
		{
			name: "missing custom endpoint",
			lookup: environment(map[string]string{
				"AI_PROVIDER": "custom",
				"AI_MODEL":    "local-model",
			}),
			wantErr: bootstrap.ErrInvalidAIConfiguration,
		},
		{
			name: "invalid insecure HTTP opt in",
			lookup: environment(map[string]string{
				"OPENAI_API_KEY":         "secret-that-must-not-leak",
				"AI_ALLOW_INSECURE_HTTP": "sometimes",
			}),
			wantErr: bootstrap.ErrInvalidAIConfiguration,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bootstrap.ResolveAIConfig(test.lookup, test.overrides)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ResolveAIConfig() error = %v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "secret-that-must-not-leak") {
				t.Fatalf("error leaked secret: %v", err)
			}
		})
	}
}

func TestNewAIReplyGeneratorBuildsWithoutCallingEndpoint(t *testing.T) {
	config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
		"AI_PROVIDER": "custom",
		"AI_MODEL":    "local-model",
		"AI_BASE_URL": "http://127.0.0.1:11434/v1",
	}), bootstrap.AIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := bootstrap.NewAIReplyGenerator(context.Background(), config)
	if err != nil || generator == nil {
		t.Fatalf("NewAIReplyGenerator() = %v, error %v", generator, err)
	}
}

func environment(values map[string]string) bootstrap.LookupEnv {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}
