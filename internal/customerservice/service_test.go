package customerservice_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

var serviceBaseTime = time.Date(2026, time.August, 6, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))

func TestReceiveMessageCreatesConversationAndMessage(t *testing.T) {
	service, repository, _ := newTestService(t)

	result, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("ReceiveMessage() error = %v", err)
	}
	if result.ConversationID != "conv-1" || result.MessageID != "message-1" {
		t.Fatalf("result IDs = conversation %q message %q", result.ConversationID, result.MessageID)
	}
	if result.Status != conversation.StatusBotActive {
		t.Fatalf("result status = %q, want bot_active", result.Status)
	}
	if result.NextAction != customerservice.NextActionGenerateBotReply || result.Duplicate {
		t.Fatalf("result routing = action %q duplicate %v", result.NextAction, result.Duplicate)
	}

	stored, err := repository.FindConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("FindConversation() error = %v", err)
	}
	if stored.CustomerID() != "customer-1" || stored.Channel() != "web" {
		t.Fatalf("stored conversation = customer %q channel %q", stored.CustomerID(), stored.Channel())
	}
	messages := listMessages(t, repository, "conv-1")
	if len(messages) != 1 || messages[0].Content() != "I need help" {
		t.Fatalf("stored messages = %#v", messages)
	}
}

func TestReceiveMessageRoutesByConversationStatus(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*conversation.Conversation) error
		wantStatus conversation.Status
		wantAction customerservice.NextAction
	}{
		{
			name:       "bot active",
			transition: func(*conversation.Conversation) error { return nil },
			wantStatus: conversation.StatusBotActive,
			wantAction: customerservice.NextActionGenerateBotReply,
		},
		{
			name: "waiting for human",
			transition: func(current *conversation.Conversation) error {
				return current.RequestHandoff(serviceBaseTime.Add(time.Minute))
			},
			wantStatus: conversation.StatusWaitingHuman,
			wantAction: customerservice.NextActionWaitForHuman,
		},
		{
			name: "human active",
			transition: func(current *conversation.Conversation) error {
				if err := current.RequestHandoff(serviceBaseTime.Add(time.Minute)); err != nil {
					return err
				}
				return current.AssignAgent("agent-1", serviceBaseTime.Add(2*time.Minute))
			},
			wantStatus: conversation.StatusHumanActive,
			wantAction: customerservice.NextActionDeliverToHuman,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository, _ := newTestService(t)
			current := serviceConversation(t)
			if err := test.transition(current); err != nil {
				t.Fatal(err)
			}
			seedConversation(t, repository, current)

			result, err := service.ReceiveMessage(context.Background(), validCommand())
			if err != nil {
				t.Fatalf("ReceiveMessage() error = %v", err)
			}
			if result.Status != test.wantStatus || result.NextAction != test.wantAction {
				t.Fatalf("result = status %q action %q, want %q %q", result.Status, result.NextAction, test.wantStatus, test.wantAction)
			}
		})
	}
}

func TestReceiveMessageRejectsClosedConversationWithoutSaving(t *testing.T) {
	service, repository, _ := newTestService(t)
	current := serviceConversation(t)
	if err := current.Close(serviceBaseTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	seedConversation(t, repository, current)

	_, err := service.ReceiveMessage(context.Background(), validCommand())
	if !errors.Is(err, conversation.ErrConversationClosed) {
		t.Fatalf("ReceiveMessage() error = %v, want ErrConversationClosed", err)
	}
	if messages := listMessages(t, repository, "conv-1"); len(messages) != 0 {
		t.Fatalf("closed conversation stored %d messages", len(messages))
	}
}

func TestReceiveMessageIsIdempotent(t *testing.T) {
	service, repository, ids := newTestService(t)
	first, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	if !second.Duplicate || second.NextAction != customerservice.NextActionNone {
		t.Fatalf("duplicate result = duplicate %v action %q", second.Duplicate, second.NextAction)
	}
	if second.MessageID != first.MessageID {
		t.Fatalf("duplicate message ID = %q, want %q", second.MessageID, first.MessageID)
	}
	if got := ids.calls.Load(); got != 1 {
		t.Fatalf("ID generator calls = %d, want 1", got)
	}
	if messages := listMessages(t, repository, "conv-1"); len(messages) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(messages))
	}
}

func TestDuplicateRemainsSuccessfulAfterConversationCloses(t *testing.T) {
	service, repository, _ := newTestService(t)
	first, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		current, findErr := tx.FindConversation(first.ConversationID)
		if findErr != nil {
			return findErr
		}
		if closeErr := current.Close(serviceBaseTime.Add(time.Minute)); closeErr != nil {
			return closeErr
		}
		return tx.UpdateConversation(current)
	})
	if err != nil {
		t.Fatal(err)
	}

	duplicate, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("duplicate after close error = %v", err)
	}
	if !duplicate.Duplicate || duplicate.Status != conversation.StatusClosed || duplicate.NextAction != customerservice.NextActionNone {
		t.Fatalf("duplicate after close = %#v", duplicate)
	}
}

func TestReceiveMessageDetectsIdempotencyConflict(t *testing.T) {
	service, _, _ := newTestService(t)
	if _, err := service.ReceiveMessage(context.Background(), validCommand()); err != nil {
		t.Fatal(err)
	}
	conflict := validCommand()
	conflict.Content = "different content"

	_, err := service.ReceiveMessage(context.Background(), conflict)
	if !errors.Is(err, customerservice.ErrIdempotencyConflict) {
		t.Fatalf("ReceiveMessage() error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestReceiveMessageRejectsConversationIdentityMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*customerservice.ReceiveMessageCommand)
	}{
		{name: "different customer", mutate: func(command *customerservice.ReceiveMessageCommand) {
			command.CustomerID = "customer-2"
		}},
		{name: "different channel", mutate: func(command *customerservice.ReceiveMessageCommand) {
			command.Channel = "mobile"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository, _ := newTestService(t)
			seedConversation(t, repository, serviceConversation(t))
			command := validCommand()
			test.mutate(&command)

			_, err := service.ReceiveMessage(context.Background(), command)
			if !errors.Is(err, customerservice.ErrConversationMismatch) {
				t.Fatalf("ReceiveMessage() error = %v, want ErrConversationMismatch", err)
			}
		})
	}
}

func TestReceiveMessageRollsBackNewConversationWhenMessageIsInvalid(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	service, err := customerservice.New(
		repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) { return " ", nil }),
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.ReceiveMessage(context.Background(), validCommand())
	if !errors.Is(err, conversation.ErrInvalidArgument) {
		t.Fatalf("ReceiveMessage() error = %v, want ErrInvalidArgument", err)
	}
	_, err = repository.FindConversation(context.Background(), "conv-1")
	if !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatalf("FindConversation() error = %v, want rollback", err)
	}
}

func TestReceiveMessageConcurrentDuplicate(t *testing.T) {
	service, repository, _ := newTestService(t)
	const goroutines = 32

	results := make(chan customerservice.ReceiveMessageResult, goroutines)
	errorsChannel := make(chan error, goroutines)
	var group sync.WaitGroup
	group.Add(goroutines)
	for range goroutines {
		go func() {
			defer group.Done()
			result, err := service.ReceiveMessage(context.Background(), validCommand())
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errorsChannel)

	for err := range errorsChannel {
		t.Errorf("ReceiveMessage() concurrent error = %v", err)
	}
	var messageID string
	nonDuplicates := 0
	for result := range results {
		if messageID == "" {
			messageID = result.MessageID
		}
		if result.MessageID != messageID {
			t.Errorf("message ID = %q, want %q", result.MessageID, messageID)
		}
		if !result.Duplicate {
			nonDuplicates++
		}
	}
	if nonDuplicates != 1 {
		t.Fatalf("non-duplicate results = %d, want 1", nonDuplicates)
	}
	if messages := listMessages(t, repository, "conv-1"); len(messages) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(messages))
	}
}

type sequenceIDGenerator struct {
	calls atomic.Int64
}

func (g *sequenceIDGenerator) NewMessageID() (string, error) {
	next := g.calls.Add(1)
	return fmt.Sprintf("message-%d", next), nil
}

func newTestService(
	t *testing.T,
) (*customerservice.Service, *conversation.MemoryRepository, *sequenceIDGenerator) {
	t.Helper()
	repository := conversation.NewMemoryRepository()
	ids := &sequenceIDGenerator{}
	service, err := customerservice.New(
		repository,
		ids,
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	return service, repository, ids
}

func validCommand() customerservice.ReceiveMessageCommand {
	return customerservice.ReceiveMessageCommand{
		ConversationID:    "conv-1",
		CustomerID:        "customer-1",
		Channel:           "web",
		ExternalMessageID: "web-message-1",
		Content:           "I need help",
	}
}

func serviceConversation(t *testing.T) *conversation.Conversation {
	t.Helper()
	current, err := conversation.New(conversation.CreateParams{
		ID:         "conv-1",
		CustomerID: "customer-1",
		Channel:    "web",
		CreatedAt:  serviceBaseTime,
	})
	if err != nil {
		t.Fatalf("conversation.New() error = %v", err)
	}
	return current
}

func seedConversation(
	t *testing.T,
	repository *conversation.MemoryRepository,
	current *conversation.Conversation,
) {
	t.Helper()
	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		return tx.InsertConversation(current)
	})
	if err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
}

func listMessages(
	t *testing.T,
	repository *conversation.MemoryRepository,
	conversationID string,
) []conversation.Message {
	t.Helper()
	messages, err := repository.ListMessages(context.Background(), conversationID)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	return messages
}
