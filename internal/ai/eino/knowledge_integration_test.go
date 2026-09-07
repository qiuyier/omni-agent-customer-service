package einoadapter_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

func TestKnowledgeFAQServiceAndEinoIntegration(t *testing.T) {
	entries := []faq.Entry{
		{ID: "returns", Title: "退货条件", Source: "faq://returns", Content: "示例退货需要保持包装完整。", Keywords: []string{"退货"}},
		{ID: "invoice", Title: "开票说明", Source: "faq://invoice", Content: "示例发票需要提供开票抬头。", Keywords: []string{"发票"}},
	}
	retriever, err := faq.New(entries)
	if err != nil {
		t.Fatal(err)
	}
	var retrievalCalls, nextID atomic.Int64
	model := &recordingAgenticModel{
		response: &schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: "generated answer"})},
		},
		stream: schema.StreamReaderFromArray([]*schema.AgenticMessage{{
			Role:          schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{schema.NewContentBlock(&schema.AssistantGenText{Text: "streamed answer"})},
		}}),
	}
	generator, err := einoadapter.NewAgenticGenerator(model, "Answer the customer using the supplied knowledge.")
	if err != nil {
		t.Fatal(err)
	}
	repository := conversation.NewMemoryRepository()
	service, err := customerservice.New(repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			return fmt.Sprintf("message-%d", nextID.Add(1)), nil
		}),
		customerservice.ClockFunc(time.Now),
		customerservice.WithReplyGenerator(generator),
		customerservice.WithReplyStreamGenerator(generator),
		customerservice.WithKnowledgeRetriever(customerservice.KnowledgeRetrieverFunc(
			func(ctx context.Context, query customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				retrievalCalls.Add(1)
				return retriever.Retrieve(ctx, query)
			},
		)),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for index, mode := range []string{"generate", "stream"} {
		t.Run(mode, func(t *testing.T) {
			question := "请说明" + entries[index].Keywords[0] + "的要求"
			received, err := service.ReceiveMessage(ctx, customerservice.ReceiveMessageCommand{
				ConversationID: "conversation-" + mode, CustomerID: "customer-" + mode,
				Channel: "web", ExternalMessageID: "external-" + mode, Content: question,
			})
			if err != nil {
				t.Fatal(err)
			}
			command := customerservice.GenerateBotReplyCommand{
				ConversationID: received.ConversationID, TriggerMessageID: received.MessageID,
			}
			reply := completeKnowledgeReply(t, service, mode, command)
			if reply.Duplicate || reply.Content == "" || len(model.input) != 4 {
				t.Fatalf("initial reply = %#v, model message count = %d", reply, len(model.input))
			}
			if model.input[2].Role != schema.AgenticRoleTypeUser || agenticMessageText(model.input[3]) != question {
				t.Fatal("references and original question were not mapped to separate user messages")
			}
			references := referenceJSON(t, agenticMessageText(model.input[2]))
			if !strings.Contains(references, entries[index].Content) || strings.Contains(references, entries[1-index].Content) {
				t.Fatalf("model did not receive only the matched FAQ reference: %s", references)
			}
			model.input = nil
			callsBeforeRetry := retrievalCalls.Load()
			duplicate := completeKnowledgeReply(t, service, mode, command)
			if !duplicate.Duplicate || duplicate.MessageID != reply.MessageID || duplicate.Content != reply.Content {
				t.Fatalf("duplicate reply = %#v, original = %#v", duplicate, reply)
			}
			if model.input != nil || retrievalCalls.Load() != callsBeforeRetry {
				t.Fatal("duplicate trigger called the model or knowledge retriever")
			}
			messages, err := repository.ListMessages(ctx, received.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 2 || messages[0].Sender() != conversation.SenderCustomer ||
				messages[0].Content() != question || messages[1].Sender() != conversation.SenderBot ||
				messages[1].Content() != reply.Content || messages[1].ReplyToMessageID() != received.MessageID {
				t.Fatalf("persisted history contains unexpected data: %#v", messages)
			}
		})
	}
	if retrievalCalls.Load() != 2 || model.streamCalls != 1 {
		t.Fatalf("retrieval calls = %d, stream model calls = %d", retrievalCalls.Load(), model.streamCalls)
	}
}

func completeKnowledgeReply(t *testing.T, service *customerservice.Service, mode string, command customerservice.GenerateBotReplyCommand) customerservice.GenerateBotReplyResult {
	t.Helper()
	if mode == "generate" {
		reply, err := service.GenerateBotReply(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		return reply
	}
	reader, err := service.StreamBotReply(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	var reply customerservice.GenerateBotReplyResult
	for {
		event, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type == customerservice.BotReplyStreamEventCompleted {
			reply = event.Result
		}
	}
	if reply.MessageID == "" {
		t.Fatal("stream ended without a persisted completion")
	}
	return reply
}
