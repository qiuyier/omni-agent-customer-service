package customerservice_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestGenerateBotReplyPersistsAndReturnsExistingReplyIdempotently(t *testing.T) {
	var calls atomic.Int64
	var captured customerservice.ReplyRequest
	generator := customerservice.ReplyGeneratorFunc(func(
		_ context.Context,
		request customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		calls.Add(1)
		captured = request
		return customerservice.Reply{Content: "I can help with that."}, nil
	})
	service, repository, ids := newReplyTestService(t, generator)
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	command := customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	}
	first, err := service.GenerateBotReply(context.Background(), command)
	if err != nil {
		t.Fatalf("GenerateBotReply() error = %v", err)
	}
	if first.MessageID != "message-2" || first.Content != "I can help with that." || first.Duplicate {
		t.Fatalf("first result = %#v", first)
	}
	if captured.ConversationID != command.ConversationID ||
		captured.TriggerMessageID != command.TriggerMessageID ||
		len(captured.History) != 1 ||
		captured.History[0].Role != customerservice.ReplyRoleUser ||
		captured.History[0].Content != validCommand().Content {
		t.Fatalf("captured request = %#v", captured)
	}

	stored, err := repository.FindReplyByTriggerMessageID(context.Background(), received.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID() != first.MessageID || stored.ReplyToMessageID() != received.MessageID {
		t.Fatalf("stored reply = ID %q trigger %q", stored.ID(), stored.ReplyToMessageID())
	}

	second, err := service.GenerateBotReply(context.Background(), command)
	if err != nil {
		t.Fatalf("duplicate GenerateBotReply() error = %v", err)
	}
	if !second.Duplicate || second.MessageID != first.MessageID || second.Content != first.Content {
		t.Fatalf("duplicate result = %#v, first = %#v", second, first)
	}
	if calls.Load() != 1 {
		t.Fatalf("generator calls = %d, want 1", calls.Load())
	}
	if ids.calls.Load() != 2 {
		t.Fatalf("ID generator calls = %d, want 2", ids.calls.Load())
	}
}

func TestExistingReplySucceedsAfterConversationCloses(t *testing.T) {
	service, repository, _ := newReplyTestService(t, customerservice.ReplyGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		return customerservice.Reply{Content: "stored reply"}, nil
	}))
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	command := customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	}
	first, err := service.GenerateBotReply(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	changeConversation(t, repository, func(current *conversation.Conversation) error {
		return current.Close(serviceBaseTime.Add(time.Minute))
	})

	duplicate, err := service.GenerateBotReply(context.Background(), command)
	if err != nil {
		t.Fatalf("duplicate after close error = %v", err)
	}
	if !duplicate.Duplicate || duplicate.MessageID != first.MessageID {
		t.Fatalf("duplicate after close = %#v", duplicate)
	}
}

func TestGenerateBotReplyRequiresGeneratorOnlyForNewReply(t *testing.T) {
	service, _, _ := newTestService(t)
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if !errors.Is(err, customerservice.ErrReplyUnavailable) {
		t.Fatalf("GenerateBotReply() error = %v, want ErrReplyUnavailable", err)
	}
}

func TestGenerateBotReplyBoundsAllProviderAttemptsWithOneDeadline(t *testing.T) {
	const timeout = 50 * time.Millisecond
	repository := conversation.NewMemoryRepository()
	ids := &sequenceIDGenerator{}
	deadlineObserved := false
	generator := customerservice.ReplyGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		deadline, found := ctx.Deadline()
		if !found {
			t.Fatal("reply generator context has no deadline")
		}
		deadlineObserved = true
		if remaining := time.Until(deadline); remaining > timeout {
			t.Fatalf("reply generator deadline remaining = %v, want at most %v", remaining, timeout)
		}
		<-ctx.Done()
		return customerservice.Reply{}, ctx.Err()
	})
	service, err := customerservice.New(
		repository,
		ids,
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyGenerator(generator),
		customerservice.WithReplyGenerationTimeout(timeout),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	_, err = service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if !errors.Is(err, context.DeadlineExceeded) ||
		!errors.Is(err, customerservice.ErrReplyGenerationFailed) {
		t.Fatalf("GenerateBotReply() error = %v, want deadline and generation failure", err)
	}
	if !deadlineObserved {
		t.Fatal("reply generator did not observe the application deadline")
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("GenerateBotReply() elapsed = %v, want bounded by one deadline", elapsed)
	}
	if messages := listMessages(t, repository, received.ConversationID); len(messages) != 1 {
		t.Fatalf("stored messages = %d, want only customer trigger", len(messages))
	}
}

func TestWithReplyGenerationTimeoutRejectsNonPositiveDuration(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		service, err := customerservice.New(
			conversation.NewMemoryRepository(),
			&sequenceIDGenerator{},
			customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
			customerservice.WithReplyGenerationTimeout(timeout),
		)
		if !errors.Is(err, customerservice.ErrInvalidDependency) {
			t.Fatalf("timeout %v: error = %v, want ErrInvalidDependency", timeout, err)
		}
		if service != nil {
			t.Fatalf("timeout %v: service = %#v, want nil", timeout, service)
		}
	}
}

func TestGenerateBotReplyRejectsInvalidOrSupersededTrigger(t *testing.T) {
	t.Run("missing trigger", func(t *testing.T) {
		var calls atomic.Int64
		service, repository, _ := newReplyTestService(t, countingReplyGenerator(&calls))
		seedConversation(t, repository, serviceConversation(t))

		_, err := service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
			ConversationID:   "conv-1",
			TriggerMessageID: "missing-message",
		})
		if !errors.Is(err, customerservice.ErrInvalidReplyTrigger) {
			t.Fatalf("GenerateBotReply() error = %v, want ErrInvalidReplyTrigger", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("generator calls = %d, want 0", calls.Load())
		}
	})

	t.Run("newer customer message", func(t *testing.T) {
		var calls atomic.Int64
		service, _, _ := newReplyTestService(t, countingReplyGenerator(&calls))
		first, err := service.ReceiveMessage(context.Background(), validCommand())
		if err != nil {
			t.Fatal(err)
		}
		secondCommand := validCommand()
		secondCommand.ExternalMessageID = "web-message-2"
		secondCommand.Content = "I also have another question"
		if _, err := service.ReceiveMessage(context.Background(), secondCommand); err != nil {
			t.Fatal(err)
		}

		_, err = service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
			ConversationID:   first.ConversationID,
			TriggerMessageID: first.MessageID,
		})
		if !errors.Is(err, customerservice.ErrReplySuperseded) {
			t.Fatalf("GenerateBotReply() error = %v, want ErrReplySuperseded", err)
		}
		if calls.Load() != 0 {
			t.Fatalf("generator calls = %d, want 0", calls.Load())
		}
	})
}

func TestGenerateBotReplyDoesNotPersistModelFailures(t *testing.T) {
	modelErr := errors.New("model unavailable")
	tests := []struct {
		name      string
		generator customerservice.ReplyGenerator
		wantErr   error
		wantClass error
	}{
		{
			name: "model error",
			generator: customerservice.ReplyGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (customerservice.Reply, error) {
				return customerservice.Reply{}, modelErr
			}),
			wantErr:   modelErr,
			wantClass: customerservice.ErrReplyGenerationFailed,
		},
		{
			name: "blank model response",
			generator: customerservice.ReplyGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (customerservice.Reply, error) {
				return customerservice.Reply{Content: "   "}, nil
			}),
			wantErr:   customerservice.ErrEmptyReply,
			wantClass: customerservice.ErrEmptyReply,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository, ids := newReplyTestService(t, test.generator)
			received, err := service.ReceiveMessage(context.Background(), validCommand())
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
				ConversationID:   received.ConversationID,
				TriggerMessageID: received.MessageID,
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("GenerateBotReply() error = %v, want %v", err, test.wantErr)
			}
			if !errors.Is(err, test.wantClass) {
				t.Fatalf("GenerateBotReply() error = %v, want class %v", err, test.wantClass)
			}
			if messages := listMessages(t, repository, received.ConversationID); len(messages) != 1 {
				t.Fatalf("stored messages = %d, want only customer trigger", len(messages))
			}
			if ids.calls.Load() != 1 {
				t.Fatalf("ID generator calls = %d, want 1", ids.calls.Load())
			}
		})
	}
}

func TestGenerateBotReplyDoesNotPersistAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	service, repository, _ := newReplyTestService(t, customerservice.ReplyGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		cancel()
		return customerservice.Reply{Content: "too late"}, nil
	}))
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GenerateBotReply(ctx, customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateBotReply() error = %v, want context.Canceled", err)
	}
	if messages := listMessages(t, repository, received.ConversationID); len(messages) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(messages))
	}
}

func TestGenerateBotReplyDiscardsContentWhenConversationChanges(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*testing.T, *customerservice.Service, *conversation.MemoryRepository)
		wantMessages int
	}{
		{
			name: "handoff requested",
			mutate: func(t *testing.T, _ *customerservice.Service, repository *conversation.MemoryRepository) {
				changeConversation(t, repository, func(current *conversation.Conversation) error {
					return current.RequestHandoff(serviceBaseTime.Add(time.Minute))
				})
			},
			wantMessages: 1,
		},
		{
			name: "handoff ABA",
			mutate: func(t *testing.T, _ *customerservice.Service, repository *conversation.MemoryRepository) {
				changeConversation(t, repository, func(current *conversation.Conversation) error {
					if err := current.RequestHandoff(serviceBaseTime.Add(time.Minute)); err != nil {
						return err
					}
					return current.CancelHandoff(serviceBaseTime.Add(2 * time.Minute))
				})
			},
			wantMessages: 1,
		},
		{
			name: "new customer message",
			mutate: func(t *testing.T, service *customerservice.Service, _ *conversation.MemoryRepository) {
				command := validCommand()
				command.ExternalMessageID = "web-message-2"
				command.Content = "new context"
				if _, err := service.ReceiveMessage(context.Background(), command); err != nil {
					t.Fatal(err)
				}
			},
			wantMessages: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generator := newBlockingReplyGenerator(1)
			service, repository, _ := newReplyTestService(t, generator)
			received, err := service.ReceiveMessage(context.Background(), validCommand())
			if err != nil {
				t.Fatal(err)
			}
			outcomeChannel := make(chan replyOutcome, 1)
			go func() {
				result, generateErr := service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
					ConversationID:   received.ConversationID,
					TriggerMessageID: received.MessageID,
				})
				outcomeChannel <- replyOutcome{result: result, err: generateErr}
			}()
			waitForGeneratorStart(t, generator.started)
			test.mutate(t, service, repository)
			close(generator.release)
			outcome := waitForReplyOutcome(t, outcomeChannel)
			if !errors.Is(outcome.err, customerservice.ErrReplySuperseded) {
				t.Fatalf("GenerateBotReply() error = %v, want ErrReplySuperseded", outcome.err)
			}
			if messages := listMessages(t, repository, received.ConversationID); len(messages) != test.wantMessages {
				t.Fatalf("stored messages = %d, want %d", len(messages), test.wantMessages)
			}
		})
	}
}

func TestGenerateBotReplyConcurrentCallsPersistOneWinner(t *testing.T) {
	generator := newBlockingReplyGenerator(2)
	service, repository, _ := newReplyTestService(t, generator)
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	command := customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	}

	const goroutines = 2
	outcomes := make(chan replyOutcome, goroutines)
	var group sync.WaitGroup
	group.Add(goroutines)
	for range goroutines {
		go func() {
			defer group.Done()
			result, generateErr := service.GenerateBotReply(context.Background(), command)
			outcomes <- replyOutcome{result: result, err: generateErr}
		}()
	}
	waitForGeneratorStart(t, generator.started)
	waitForGeneratorStart(t, generator.started)
	close(generator.release)
	group.Wait()
	close(outcomes)

	var winnerID string
	duplicates := 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent GenerateBotReply() error = %v", outcome.err)
		}
		if winnerID == "" {
			winnerID = outcome.result.MessageID
		}
		if outcome.result.MessageID != winnerID {
			t.Fatalf("reply ID = %q, want winner %q", outcome.result.MessageID, winnerID)
		}
		if outcome.result.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicate results = %d, want 1", duplicates)
	}
	if generator.calls.Load() != 2 {
		t.Fatalf("generator calls = %d, want 2", generator.calls.Load())
	}
	if messages := listMessages(t, repository, received.ConversationID); len(messages) != 2 {
		t.Fatalf("stored messages = %d, want customer plus one reply", len(messages))
	}
}

func TestGenerateBotReplyLocksConversationBeforeReplyLookup(t *testing.T) {
	lookupBeforeLock := errors.New("reply lookup happened before conversation lock")
	baseRepository := conversation.NewMemoryRepository()
	repository := &lockOrderRepository{
		Repository:       baseRepository,
		lookupBeforeLock: lookupBeforeLock,
	}
	service, err := customerservice.New(
		repository,
		&sequenceIDGenerator{},
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			return customerservice.Reply{Content: "answer"}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if errors.Is(err, lookupBeforeLock) || err != nil {
		t.Fatalf("GenerateBotReply() error = %v", err)
	}
}

func TestGenerateBotReplyMapsCompleteConversationHistory(t *testing.T) {
	var captured customerservice.ReplyRequest
	generator := customerservice.ReplyGeneratorFunc(func(
		_ context.Context,
		request customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		captured = request
		return customerservice.Reply{Content: "answer"}, nil
	})
	repository := conversation.NewMemoryRepository()
	current := serviceConversation(t)
	firstCustomer := mustMessage(t, conversation.MessageParams{
		ID:                "customer-1",
		ConversationID:    current.ID(),
		ExternalMessageID: "web-1",
		Sender:            conversation.SenderCustomer,
		SenderID:          current.CustomerID(),
		Content:           "first question",
		CreatedAt:         serviceBaseTime,
	})
	firstReply := mustMessage(t, conversation.MessageParams{
		ID:               "bot-1",
		ConversationID:   current.ID(),
		ReplyToMessageID: firstCustomer.ID(),
		Sender:           conversation.SenderBot,
		Content:          "first answer",
		CreatedAt:        serviceBaseTime,
	})
	systemMessage := mustMessage(t, conversation.MessageParams{
		ID:             "system-1",
		ConversationID: current.ID(),
		Sender:         conversation.SenderSystem,
		Content:        "customer is premium",
		CreatedAt:      serviceBaseTime,
	})
	toolMessage := mustMessage(t, conversation.MessageParams{
		ID:             "tool-1",
		ConversationID: current.ID(),
		Sender:         conversation.SenderTool,
		Content:        "order is in transit",
		CreatedAt:      serviceBaseTime,
	})
	trigger := mustMessage(t, conversation.MessageParams{
		ID:                "customer-2",
		ConversationID:    current.ID(),
		ExternalMessageID: "web-2",
		Sender:            conversation.SenderCustomer,
		SenderID:          current.CustomerID(),
		Content:           "where is it now?",
		CreatedAt:         serviceBaseTime,
	})
	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(current); err != nil {
			return err
		}
		for _, message := range []conversation.Message{firstCustomer, firstReply, systemMessage, toolMessage, trigger} {
			if err := tx.InsertMessage(current.Channel(), message); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := customerservice.New(
		repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) { return "generated-reply", nil }),
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyGenerator(generator),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   current.ID(),
		TriggerMessageID: trigger.ID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []customerservice.ReplyTurn{
		{Role: customerservice.ReplyRoleUser, Content: "first question"},
		{Role: customerservice.ReplyRoleAssistant, Content: "first answer"},
		{Role: customerservice.ReplyRoleSystem, Content: "customer is premium"},
		{Role: customerservice.ReplyRoleSystem, Content: "Tool result: order is in transit"},
		{Role: customerservice.ReplyRoleUser, Content: "where is it now?"},
	}
	if len(captured.History) != len(want) {
		t.Fatalf("history length = %d, want %d: %#v", len(captured.History), len(want), captured.History)
	}
	for index := range want {
		if captured.History[index] != want[index] {
			t.Fatalf("history[%d] = %#v, want %#v", index, captured.History[index], want[index])
		}
	}
}

type blockingReplyGenerator struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func newBlockingReplyGenerator(capacity int) *blockingReplyGenerator {
	return &blockingReplyGenerator{
		started: make(chan struct{}, capacity),
		release: make(chan struct{}),
	}
}

func (g *blockingReplyGenerator) Generate(
	ctx context.Context,
	_ customerservice.ReplyRequest,
) (customerservice.Reply, error) {
	g.calls.Add(1)
	g.started <- struct{}{}
	select {
	case <-g.release:
		return customerservice.Reply{Content: "generated answer"}, nil
	case <-ctx.Done():
		return customerservice.Reply{}, ctx.Err()
	}
}

type replyOutcome struct {
	result customerservice.GenerateBotReplyResult
	err    error
}

type lockOrderRepository struct {
	conversation.Repository
	lookupBeforeLock error
}

func (r *lockOrderRepository) WithinTx(
	ctx context.Context,
	fn func(conversation.Transaction) error,
) error {
	return r.Repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		return fn(&lockOrderTransaction{
			Transaction:      tx,
			lookupBeforeLock: r.lookupBeforeLock,
		})
	})
}

type lockOrderTransaction struct {
	conversation.Transaction
	conversationLocked bool
	lookupBeforeLock   error
}

func (tx *lockOrderTransaction) FindConversation(id string) (*conversation.Conversation, error) {
	current, err := tx.Transaction.FindConversation(id)
	if err == nil {
		tx.conversationLocked = true
	}
	return current, err
}

func (tx *lockOrderTransaction) FindReplyByTriggerMessageID(triggerMessageID string) (conversation.Message, error) {
	if !tx.conversationLocked {
		return conversation.Message{}, tx.lookupBeforeLock
	}
	return tx.Transaction.FindReplyByTriggerMessageID(triggerMessageID)
}

func newReplyTestService(
	t *testing.T,
	generator customerservice.ReplyGenerator,
) (*customerservice.Service, *conversation.MemoryRepository, *sequenceIDGenerator) {
	t.Helper()
	repository := conversation.NewMemoryRepository()
	ids := &sequenceIDGenerator{}
	service, err := customerservice.New(
		repository,
		ids,
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyGenerator(generator),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	return service, repository, ids
}

func countingReplyGenerator(calls *atomic.Int64) customerservice.ReplyGenerator {
	return customerservice.ReplyGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		calls.Add(1)
		return customerservice.Reply{Content: "answer"}, nil
	})
}

func changeConversation(
	t *testing.T,
	repository *conversation.MemoryRepository,
	change func(*conversation.Conversation) error,
) {
	t.Helper()
	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		current, err := tx.FindConversation("conv-1")
		if err != nil {
			return err
		}
		if err := change(current); err != nil {
			return err
		}
		return tx.UpdateConversation(current)
	})
	if err != nil {
		t.Fatalf("change conversation: %v", err)
	}
}

func waitForGeneratorStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for reply generator")
	}
}

func waitForReplyOutcome(t *testing.T, outcomes <-chan replyOutcome) replyOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for GenerateBotReply")
		return replyOutcome{}
	}
}

func mustMessage(t *testing.T, params conversation.MessageParams) conversation.Message {
	t.Helper()
	message, err := conversation.NewMessage(params)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	return message
}
