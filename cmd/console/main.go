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

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "console: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	message := flag.String("message", "我的订单还没到，应该怎么办？", "customer message")
	providerName := flag.String("provider", "", "AI provider override; defaults to AI_PROVIDER")
	protocolName := flag.String("protocol", "", "AI protocol override: responses or chat_completions")
	modelName := flag.String("model", "", "provider model override; defaults to AI_MODEL")
	baseURL := flag.String("base-url", "", "provider API base URL override; defaults to AI_BASE_URL")
	flag.Parse()
	if strings.TrimSpace(*message) == "" {
		return errors.New("message must not be blank")
	}
	config, err := bootstrap.ResolveAIConfig(os.LookupEnv, bootstrap.AIOverrides{
		Provider: *providerName,
		Protocol: *protocolName,
		Model:    *modelName,
		BaseURL:  *baseURL,
	})
	if err != nil {
		return fmt.Errorf("resolve AI configuration: %w", err)
	}
	knowledgeConfig, err := bootstrap.ResolveKnowledgeConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve knowledge configuration: %w", err)
	}
	knowledgeRetriever, err := bootstrap.NewKnowledgeRetriever(knowledgeConfig)
	if err != nil {
		return fmt.Errorf("create knowledge retriever: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	generator, err := bootstrap.NewAIReplyGenerator(ctx, config)
	if err != nil {
		return err
	}

	serviceOptions := []customerservice.Option{
		customerservice.WithReplyGenerator(generator),
		customerservice.WithReplyGenerationTimeout(bootstrap.DefaultAIReplyTimeout),
	}
	if knowledgeRetriever != nil {
		serviceOptions = append(serviceOptions, customerservice.WithKnowledgeRetriever(knowledgeRetriever))
	}
	service, err := customerservice.New(
		conversation.NewMemoryRepository(),
		&sequenceIDGenerator{},
		customerservice.ClockFunc(time.Now),
		serviceOptions...,
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
