// Command console runs one end-to-end customer-service turn against the
// configured AI provider.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const defaultSystemPrompt = `你是企业智能客服。请使用与客户相同的语言回答，表达清晰、友好、简洁。
只根据对话中已知的信息作答；信息不足时提出一个明确的澄清问题，不要编造订单、政策或处理结果。`

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "console: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	message := flag.String("message", "我的订单还没到，应该怎么办？", "customer message")
	providerName := flag.String("provider", environmentOrDefault("AI_PROVIDER", "openai"), "AI provider preset")
	protocolName := flag.String("protocol", os.Getenv("AI_PROTOCOL"), "AI protocol: responses or chat_completions")
	modelName := flag.String("model", os.Getenv("AI_MODEL"), "provider model name")
	baseURL := flag.String("base-url", os.Getenv("AI_BASE_URL"), "provider API base URL override")
	flag.Parse()
	if strings.TrimSpace(*message) == "" {
		return errors.New("message must not be blank")
	}
	provider, err := einoadapter.ParseProvider(*providerName)
	if err != nil {
		return err
	}
	model := strings.TrimSpace(*modelName)
	endpoint := strings.TrimSpace(*baseURL)
	if provider == einoadapter.ProviderOpenAI {
		model = firstNonBlank(model, os.Getenv("OPENAI_MODEL"))
		endpoint = firstNonBlank(endpoint, os.Getenv("OPENAI_BASE_URL"))
	}
	apiKeyEnvironment, err := einoadapter.ProviderAPIKeyEnvironmentVariable(provider)
	if err != nil {
		return err
	}
	apiKey := firstNonBlank(os.Getenv("AI_API_KEY"), os.Getenv(apiKeyEnvironment))
	if apiKey == "" && provider != einoadapter.ProviderCustom {
		if apiKeyEnvironment == "" {
			return errors.New("AI_API_KEY must be set")
		}
		return fmt.Errorf("AI_API_KEY or %s must be set", apiKeyEnvironment)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	generator, err := einoadapter.NewProviderGenerator(ctx, einoadapter.ProviderConfig{
		Provider:     provider,
		Protocol:     einoadapter.Protocol(strings.TrimSpace(*protocolName)),
		APIKey:       apiKey,
		Model:        model,
		BaseURL:      endpoint,
		Timeout:      75 * time.Second,
		SystemPrompt: defaultSystemPrompt,
		Headers:      providerHeaders(provider),
	})
	if err != nil {
		return err
	}

	service, err := customerservice.New(
		conversation.NewMemoryRepository(),
		&sequenceIDGenerator{},
		customerservice.ClockFunc(time.Now),
		customerservice.WithReplyGenerator(generator),
	)
	if err != nil {
		return err
	}
	received, err := service.ReceiveMessage(ctx, customerservice.ReceiveMessageCommand{
		ConversationID:    "console-conversation",
		CustomerID:        "console-customer",
		Channel:           "console",
		ExternalMessageID: "console-message",
		Content:           *message,
	})
	if err != nil {
		return fmt.Errorf("receive customer message: %w", err)
	}
	reply, err := service.GenerateBotReply(ctx, customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if err != nil {
		return fmt.Errorf("generate reply: %w", err)
	}
	fmt.Println(reply.Content)
	return nil
}

type sequenceIDGenerator struct {
	next atomic.Uint64
}

func (g *sequenceIDGenerator) NewMessageID() (string, error) {
	return fmt.Sprintf("message-%d", g.next.Add(1)), nil
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func providerHeaders(provider einoadapter.Provider) map[string]string {
	if provider != einoadapter.ProviderOpenRouter {
		return nil
	}
	headers := make(map[string]string, 2)
	if value := strings.TrimSpace(os.Getenv("OPENROUTER_SITE_URL")); value != "" {
		headers["HTTP-Referer"] = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENROUTER_APP_NAME")); value != "" {
		headers["X-OpenRouter-Title"] = value
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}
