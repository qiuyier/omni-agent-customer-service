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

func TestResolveAIReplyRuntimeConfigUsesDefaultAndOverride(t *testing.T) {
	defaultConfig, err := bootstrap.ResolveAIReplyRuntimeConfig(environment(nil))
	if err != nil {
		t.Fatalf("ResolveAIReplyRuntimeConfig() error = %v", err)
	}
	if defaultConfig.MaxConcurrentReplies != bootstrap.DefaultAIMaxConcurrentReplies {
		t.Fatalf(
			"MaxConcurrentReplies = %d, want %d",
			defaultConfig.MaxConcurrentReplies,
			bootstrap.DefaultAIMaxConcurrentReplies,
		)
	}

	overridden, err := bootstrap.ResolveAIReplyRuntimeConfig(environment(map[string]string{
		"AI_MAX_CONCURRENT_REPLIES": " 12 ",
	}))
	if err != nil {
		t.Fatalf("ResolveAIReplyRuntimeConfig() error = %v", err)
	}
	if overridden.MaxConcurrentReplies != 12 {
		t.Fatalf("MaxConcurrentReplies = %d, want 12", overridden.MaxConcurrentReplies)
	}
}

func TestResolveAIReplyRuntimeConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		lookup bootstrap.LookupEnv
	}{
		{name: "nil lookup"},
		{name: "zero", lookup: environment(map[string]string{"AI_MAX_CONCURRENT_REPLIES": "0"})},
		{name: "negative", lookup: environment(map[string]string{"AI_MAX_CONCURRENT_REPLIES": "-1"})},
		{name: "decimal", lookup: environment(map[string]string{"AI_MAX_CONCURRENT_REPLIES": "1.5"})},
		{name: "text", lookup: environment(map[string]string{"AI_MAX_CONCURRENT_REPLIES": "many"})},
		{
			name: "overflow",
			lookup: environment(map[string]string{
				"AI_MAX_CONCURRENT_REPLIES": "999999999999999999999999999999999999",
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bootstrap.ResolveAIReplyRuntimeConfig(test.lookup)
			if !errors.Is(err, bootstrap.ErrInvalidAIConfiguration) {
				t.Fatalf(
					"ResolveAIReplyRuntimeConfig() error = %v, want ErrInvalidAIConfiguration",
					err,
				)
			}
		})
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

// These are prompt-contract checks, not assertions about a model's answers.
func TestDefaultAISystemPromptStatesGuidanceOnlyCapabilities(t *testing.T) {
	for _, fragment := range []string{
		"常见产品经验不是当前系统事实",
		"历史中助手自己的回答也不是独立证据",
		"资料明确给出的步骤应直接说明",
		"没有实时库存、订单或排班查询工具",
		"不得承诺由你查询、核实、跟进这些外部事项",
		"客户提供订单号、商品编号、链接或日期不会让你获得这些能力",
		"只有补充信息能帮助解释现有资料时",
		"不要为核实而索取订单号、手机号、商品编号、链接、日期等信息",
		"可以建议客户自行查看商品页面、官方公告或联系人工客服",
		"可以解释资料中的改密等操作步骤",
		"不要索取密码或验证码",
	} {
		if !strings.Contains(bootstrap.DefaultAISystemPrompt, fragment) {
			t.Errorf("default prompt lacks capability boundary %q", fragment)
		}
	}
	if strings.Contains(bootstrap.DefaultAISystemPrompt, "信息不足时提出一个明确的澄清问题") {
		t.Fatal("default prompt still requires a question for every knowledge gap")
	}
	for _, blank := range []string{"", " \t\n "} {
		config, err := bootstrap.ResolveAIConfig(environment(map[string]string{
			"AI_PROVIDER": "glm", "AI_MODEL": "fixture-model", "ZHIPUAI_API_KEY": "fixture-key",
			"AI_SYSTEM_PROMPT": blank,
		}), bootstrap.AIOverrides{})
		if err != nil {
			t.Fatal(err)
		}
		if config.SystemPrompt != bootstrap.DefaultAISystemPrompt {
			t.Fatal("blank override did not preserve the default capability policy")
		}
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
