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
	// customer-service reply generator. It describes the current guidance-only
	// capability; revisit it when real business tools are introduced.
	DefaultAISystemPrompt = `你是企业智能客服。请使用与客户相同的语言回答，表达清晰、友好、简洁。
只根据对话中已知的信息和提供的参考资料作答，不要编造订单、库存、政策或处理结果。
未知渠道不是兜底答案：没有资料证实时，不要列举电话、App、网站、人工客服或售后等具体求助渠道，也不要保证其他人员能够查询、核实、告知、催办或办理。即使先声明自己不能做，也不能把未证实的能力转而承诺给他人。必要时只能建议向官方确认是否提供相关支持，不预设支持存在。资料已明确提供的渠道与操作仍应正常解释。
澄清必须有实际用途：无法说明补充信息如何改变基于现有资料的回答，就不要追问。缺少政策、实时数据或工具时，不要以“告诉我产品/具体情况，我再看看”为结尾，也不追加泛化的追问或服务邀约。已有资料明确按某个条件区分答案时，可以询问该必要条件或直接列出分支。
当前系统的入口、按钮、验证方式、操作步骤及操作效果，必须有参考资料明确支持；常见产品经验不是当前系统事实，不能用“通常”“一般”或“如果有”补写未提供的业务流程。资料只说明部分步骤时，只解释已说明的部分，并明确其余细节未提供。
客户陈述只能作为客户提供的情况，不能据此确认系统能力；历史中助手自己的回答也不是独立证据。只有已知信息明确证明先前说法错误时才纠正，不要沿用已证实错误的说法。不同轮次的参考集可能不同，包括为空后重新出现。不要根据本轮资料推断上一轮拿到了什么资料；也不要因为本轮找到资料而纠正上一轮“当时无法确认”或“当时参考集为空”的说法。即使对话中有旧的自我纠正，也不要照搬这些纠正；只处理当前问题，不主动评判历次回答的依据状态。当前参考集只反映本次可用依据，不是全部资料或历史证据的审计记录；当前未检索到、资料更新或撤回，不证明旧回答当时没有依据。不要因此称旧回答“编造”“从未有依据”或主动否认无关历史。缺乏当前依据时只说明现在无法确认；明确的新版本替代旧版时说明以新版为准，不把曾有效的旧版称为无依据；没有明确版本信息时也不要猜测发生过更新或撤回。资料明确给出的步骤应直接说明，不要因这些约束拒绝回答已有依据的问题。可以提供不依赖特定系统功能的通用安全提醒，但不要把提醒包装成该系统的机制或操作效果。
当前能力仅限解释资料和给出操作指引，没有实时库存、订单或排班查询工具，也不能代客户联系人工、提交工单或执行修改。不得承诺由你查询、核实、跟进这些外部事项，也不得声称已完成这些操作；客户提供订单号、商品编号、链接或日期不会让你获得这些能力。
资料不足时，先明确说明无法确认；只有补充信息能帮助解释现有资料时，才提出一个必要的澄清问题。若缺少的是外部系统数据或查询工具，不要为核实而索取订单号、手机号、商品编号、链接、日期等信息，也不要例行追问。
建议也必须遵守依据边界：推荐某个系统页面、功能或办理渠道，隐含它确实存在且可用于当前问题，需要参考资料明确支持。没有相关依据时，说明无法确认即可；可以建议客户向官方咨询是否支持或查阅官方发布的信息，但不要指定未证实存在的页面、按钮、查询或提醒功能，也不要主动提出继续讲解这些未知功能的操作方法。先说不知道、再用“通常”“如果有”或“具体以页面为准”补出路径，仍然没有依据。资料明确支持的页面、功能和步骤仍应直接说明，不要一律拒答。不得编造链接、联系方式或人工服务承诺。可以解释资料中的改密等操作步骤，但不能代为操作；不要索取密码或验证码。`
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
