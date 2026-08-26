package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	postgresrepository "github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres/migrations"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres/schema"
)

const testDatabaseEnvironment = "TEST_DATABASE_URL"

var integrationTime = time.Date(2026, time.August, 24, 10, 0, 0, 123456000, time.UTC)

func TestRepositoryCommitRollbackAndReload(t *testing.T) {
	repository := openTestRepository(t, true)
	ctx := context.Background()
	current := newConversation(t, "conversation-1", "customer-1", "web")
	message := newCustomerMessage(t, "message-1", current.ID(), "external-1", "hello")

	if err := repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(current); err != nil {
			return err
		}
		return tx.InsertMessage(current.Channel(), message)
	}); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}

	// A separately opened pool proves the data is durable and not retained in
	// Repository process memory.
	reloadedRepository := openTestRepository(t, false)
	reloaded, err := reloadedRepository.FindConversation(ctx, current.ID())
	if err != nil {
		t.Fatalf("FindConversation() error = %v", err)
	}
	assertConversationEqual(t, reloaded, current)
	messages, err := reloadedRepository.ListMessages(ctx, current.ID())
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].ID() != message.ID() {
		t.Fatalf("messages = %v, want [%q]", messageIDs(messages), message.ID())
	}

	rollbackMarker := errors.New("rollback marker")
	rolledBack := newCustomerMessage(t, "message-2", current.ID(), "external-2", "do not save")
	err = repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		if err := tx.InsertMessage(current.Channel(), rolledBack); err != nil {
			return err
		}
		return rollbackMarker
	})
	if !errors.Is(err, rollbackMarker) {
		t.Fatalf("WithinTx() error = %v, want rollback marker", err)
	}
	if _, err := repository.FindMessageByExternalID(ctx, current.Channel(), "external-2"); !errors.Is(err, conversation.ErrMessageNotFound) {
		t.Fatalf("rolled-back message error = %v, want ErrMessageNotFound", err)
	}

	if err := repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		stored, findErr := tx.FindConversation(current.ID())
		if findErr != nil {
			return findErr
		}
		if transitionErr := stored.RequestHandoff(integrationTime.Add(time.Minute)); transitionErr != nil {
			return transitionErr
		}
		return tx.UpdateConversation(stored)
	}); err != nil {
		t.Fatalf("update conversation: %v", err)
	}
	updated, err := repository.FindConversation(ctx, current.ID())
	if err != nil {
		t.Fatalf("find updated conversation: %v", err)
	}
	if updated.Status() != conversation.StatusWaitingHuman || updated.Revision() != 2 {
		t.Fatalf("updated status/revision = %s/%d", updated.Status(), updated.Revision())
	}
}

func TestRepositoryPreservesAppendOrderAndReplyUniqueness(t *testing.T) {
	repository := openTestRepository(t, true)
	ctx := context.Background()
	current := newConversation(t, "conversation-order", "customer-1", "web")
	first := newCustomerMessage(t, "z-message", current.ID(), "external-z", "first")
	second := newCustomerMessage(t, "a-message", current.ID(), "external-a", "second")

	if err := repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(current); err != nil {
			return err
		}
		if err := tx.InsertMessage(current.Channel(), first); err != nil {
			return err
		}
		return tx.InsertMessage(current.Channel(), second)
	}); err != nil {
		t.Fatalf("insert ordered messages: %v", err)
	}

	messages, err := repository.ListMessages(ctx, current.ID())
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].ID() != first.ID() || messages[1].ID() != second.ID() {
		t.Fatalf("message order = %v, want [%s %s]", messageIDs(messages), first.ID(), second.ID())
	}

	reply := newBotReply(t, "reply-1", current.ID(), second.ID(), "answer")
	if err := repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		return tx.InsertMessage(current.Channel(), reply)
	}); err != nil {
		t.Fatalf("insert first reply: %v", err)
	}
	duplicateReply := newBotReply(t, "reply-2", current.ID(), second.ID(), "another answer")
	err = repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		return tx.InsertMessage(current.Channel(), duplicateReply)
	})
	if !errors.Is(err, conversation.ErrReplyAlreadyExists) {
		t.Fatalf("duplicate reply error = %v, want ErrReplyAlreadyExists", err)
	}
	winner, err := repository.FindReplyByTriggerMessageID(ctx, second.ID())
	if err != nil {
		t.Fatalf("FindReplyByTriggerMessageID() error = %v", err)
	}
	if winner.ID() != reply.ID() {
		t.Fatalf("winning reply ID = %q, want %q", winner.ID(), reply.ID())
	}
}

func TestRepositoryExternalMessageIDIsChannelScoped(t *testing.T) {
	repository := openTestRepository(t, true)
	ctx := context.Background()
	for _, test := range []struct {
		conversationID string
		channel        string
		messageID      string
	}{
		{conversationID: "web-conversation", channel: "web", messageID: "web-message"},
		{conversationID: "app-conversation", channel: "app", messageID: "app-message"},
	} {
		current := newConversation(t, test.conversationID, "customer-1", test.channel)
		message := newCustomerMessage(t, test.messageID, current.ID(), "shared-external", "hello")
		if err := repository.WithinTx(ctx, func(tx conversation.Transaction) error {
			if err := tx.InsertConversation(current); err != nil {
				return err
			}
			return tx.InsertMessage(current.Channel(), message)
		}); err != nil {
			t.Fatalf("insert %s message: %v", test.channel, err)
		}
	}

	for _, channel := range []string{"web", "app"} {
		message, err := repository.FindMessageByExternalID(ctx, channel, "shared-external")
		if err != nil {
			t.Fatalf("find %s message: %v", channel, err)
		}
		if message.ID() != channel+"-message" {
			t.Fatalf("%s message ID = %q", channel, message.ID())
		}
	}
}

func TestReceiveMessageConcurrentDuplicateAcrossPools(t *testing.T) {
	firstRepository := openTestRepository(t, true)
	secondRepository := openTestRepository(t, false)
	services := newConcurrentServices(t, firstRepository, secondRepository)

	const goroutineCount = 32
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan customerservice.ReceiveMessageResult, goroutineCount)
	errorsChannel := make(chan error, goroutineCount)
	var waitGroup sync.WaitGroup
	for index := 0; index < goroutineCount; index++ {
		waitGroup.Add(1)
		service := services[index%len(services)]
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := service.ReceiveMessage(ctx, validReceiveCommand("external-shared", "hello"))
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	for err := range errorsChannel {
		t.Errorf("ReceiveMessage() concurrent error = %v", err)
	}
	var (
		winningMessageID string
		newCount         int
		duplicateCount   int
	)
	for result := range results {
		if winningMessageID == "" {
			winningMessageID = result.MessageID
		}
		if result.MessageID != winningMessageID {
			t.Errorf("message ID = %q, want winner %q", result.MessageID, winningMessageID)
		}
		if result.Duplicate {
			duplicateCount++
		} else {
			newCount++
		}
	}
	if newCount != 1 || duplicateCount != goroutineCount-1 {
		t.Fatalf("new = %d, duplicate = %d", newCount, duplicateCount)
	}
	messages, err := firstRepository.ListMessages(ctx, "conversation-1")
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("stored messages = %d, want 1", len(messages))
	}
}

func TestReceiveMessageConcurrentConversationCreationAcrossPools(t *testing.T) {
	firstRepository := openTestRepository(t, true)
	secondRepository := openTestRepository(t, false)
	services := newConcurrentServices(t, firstRepository, secondRepository)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for index, externalID := range []string{"external-a", "external-b"} {
		waitGroup.Add(1)
		service := services[index]
		go func(externalMessageID string) {
			defer waitGroup.Done()
			<-start
			_, err := service.ReceiveMessage(
				ctx,
				validReceiveCommand(externalMessageID, externalMessageID),
			)
			errorsChannel <- err
		}(externalID)
	}
	close(start)
	waitGroup.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("ReceiveMessage() concurrent creation error = %v", err)
		}
	}

	messages, err := firstRepository.ListMessages(ctx, "conversation-1")
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("stored messages = %d, want 2", len(messages))
	}
}

func TestReceiveMessageConcurrentIdempotencyConflictRollsBackLoser(t *testing.T) {
	firstRepository := openTestRepository(t, true)
	secondRepository := openTestRepository(t, false)
	services := newConcurrentServices(t, firstRepository, secondRepository)
	commands := []customerservice.ReceiveMessageCommand{
		{
			ConversationID:    "conversation-a",
			CustomerID:        "customer-1",
			Channel:           "web",
			ExternalMessageID: "shared-external",
			Content:           "payload-a",
		},
		{
			ConversationID:    "conversation-b",
			CustomerID:        "customer-1",
			Channel:           "web",
			ExternalMessageID: "shared-external",
			Content:           "payload-b",
		},
	}

	type outcome struct {
		command customerservice.ReceiveMessageCommand
		result  customerservice.ReceiveMessageResult
		err     error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	outcomes := make(chan outcome, len(commands))
	var waitGroup sync.WaitGroup
	for index, command := range commands {
		waitGroup.Add(1)
		service := services[index]
		go func(command customerservice.ReceiveMessageCommand) {
			defer waitGroup.Done()
			<-start
			result, err := service.ReceiveMessage(ctx, command)
			outcomes <- outcome{command: command, result: result, err: err}
		}(command)
	}
	close(start)
	waitGroup.Wait()
	close(outcomes)

	var winner, loser string
	for current := range outcomes {
		switch {
		case current.err == nil:
			winner = current.command.ConversationID
		case errors.Is(current.err, customerservice.ErrIdempotencyConflict):
			loser = current.command.ConversationID
		default:
			t.Fatalf("unexpected concurrent outcome: command = %#v, error = %v", current.command, current.err)
		}
	}
	if winner == "" || loser == "" || winner == loser {
		t.Fatalf("winner = %q, loser = %q", winner, loser)
	}
	if _, err := firstRepository.FindConversation(ctx, loser); !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatalf("losing conversation error = %v, want ErrConversationNotFound", err)
	}
	messages, err := firstRepository.ListMessages(ctx, winner)
	if err != nil {
		t.Fatalf("list winning messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ExternalMessageID() != "shared-external" {
		t.Fatalf("winning messages = %#v", messages)
	}
}

func TestGenerateBotReplyConcurrentAcrossPools(t *testing.T) {
	firstRepository := openTestRepository(t, true)
	secondRepository := openTestRepository(t, false)
	generator := newSynchronizedReplyGenerator()
	var sequence atomic.Uint64
	ids := customerservice.MessageIDGeneratorFunc(func() (string, error) {
		return fmt.Sprintf("message-%06d", sequence.Add(1)), nil
	})
	clock := customerservice.ClockFunc(func() time.Time { return integrationTime })
	services := make([]*customerservice.Service, 0, 2)
	for _, repository := range []conversation.Repository{firstRepository, secondRepository} {
		service, err := customerservice.New(
			repository,
			ids,
			clock,
			customerservice.WithReplyGenerator(generator),
		)
		if err != nil {
			t.Fatalf("customerservice.New() error = %v", err)
		}
		services = append(services, service)
	}

	received, err := services[0].ReceiveMessage(
		context.Background(),
		validReceiveCommand("reply-trigger-external", "question"),
	)
	if err != nil {
		t.Fatalf("ReceiveMessage() error = %v", err)
	}
	command := customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan customerservice.GenerateBotReplyResult, 2)
	errorsChannel := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, service := range services {
		waitGroup.Add(1)
		go func(service *customerservice.Service) {
			defer waitGroup.Done()
			result, generateErr := service.GenerateBotReply(ctx, command)
			if generateErr != nil {
				errorsChannel <- generateErr
				return
			}
			results <- result
		}(service)
	}
	for range 2 {
		select {
		case <-generator.entered:
		case <-ctx.Done():
			t.Fatalf("both generators did not start: %v", ctx.Err())
		}
	}
	close(generator.release)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("GenerateBotReply() concurrent error = %v", err)
	}

	var (
		winningMessageID string
		newCount         int
		duplicateCount   int
	)
	for result := range results {
		if winningMessageID == "" {
			winningMessageID = result.MessageID
		}
		if result.MessageID != winningMessageID || result.Content != "synchronized answer" {
			t.Errorf("reply result = %#v, winner = %q", result, winningMessageID)
		}
		if result.Duplicate {
			duplicateCount++
		} else {
			newCount++
		}
	}
	if generator.calls.Load() != 2 || newCount != 1 || duplicateCount != 1 {
		t.Fatalf(
			"model calls = %d, new = %d, duplicate = %d",
			generator.calls.Load(),
			newCount,
			duplicateCount,
		)
	}
	messages, err := firstRepository.ListMessages(ctx, received.ConversationID)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[1].ID() != winningMessageID {
		t.Fatalf("stored messages = %v, winning reply = %q", messageIDs(messages), winningMessageID)
	}
}

func TestRepositoryCheckSchemaRejectsVersionMismatch(t *testing.T) {
	repository := openTestRepository(t, true)
	connection := openRawTestConnection(t)
	ctx := context.Background()
	if _, err := connection.Exec(
		ctx,
		"UPDATE public.customer_service_schema_version SET version = 0",
	); err != nil {
		t.Fatalf("set mismatched schema version: %v", err)
	}
	t.Cleanup(func() {
		if _, err := connection.Exec(
			context.Background(),
			"UPDATE public.customer_service_schema_version SET version = $1",
			schema.CurrentVersion,
		); err != nil {
			t.Errorf("restore schema version: %v", err)
		}
	})

	if err := repository.CheckSchema(ctx); !errors.Is(err, postgresrepository.ErrSchemaNotReady) {
		t.Fatalf("CheckSchema() error = %v, want ErrSchemaNotReady", err)
	}
}

func openTestRepository(t *testing.T, reset bool) *postgresrepository.Repository {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(testDatabaseEnvironment))
	if databaseURL == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", testDatabaseEnvironment)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseEnvironment, err)
	}
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect for migrations: %v", err)
	}
	var databaseName string
	if err := connection.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("read test database name: %v", err)
	}
	if !strings.HasSuffix(strings.ToLower(databaseName), "_test") {
		_ = connection.Close(ctx)
		t.Fatalf(
			"refusing to migrate or truncate database %q: test database name must end with _test",
			databaseName,
		)
	}
	if _, err := migrations.Up(ctx, connection); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("apply migrations: %v", err)
	}
	if reset {
		if _, err := connection.Exec(
			ctx,
			"TRUNCATE customer_service.messages, customer_service.conversations RESTART IDENTITY",
		); err != nil {
			_ = connection.Close(ctx)
			t.Fatalf("reset database: %v", err)
		}
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatalf("close setup connection: %v", err)
	}

	repository, err := postgresrepository.Open(ctx, postgresrepository.Config{DatabaseURL: databaseURL})
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(repository.Close)
	return repository
}

func openRawTestConnection(t *testing.T) *pgx.Conn {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(testDatabaseEnvironment))
	if databaseURL == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", testDatabaseEnvironment)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseEnvironment, err)
	}
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect raw test database: %v", err)
	}
	t.Cleanup(func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := connection.Close(closeContext); err != nil {
			t.Errorf("close raw test connection: %v", err)
		}
	})
	return connection
}

func newConcurrentServices(
	t *testing.T,
	repositories ...conversation.Repository,
) []*customerservice.Service {
	t.Helper()
	var sequence atomic.Uint64
	ids := customerservice.MessageIDGeneratorFunc(func() (string, error) {
		return fmt.Sprintf("message-%06d", sequence.Add(1)), nil
	})
	clock := customerservice.ClockFunc(func() time.Time { return integrationTime })
	services := make([]*customerservice.Service, 0, len(repositories))
	for _, repository := range repositories {
		service, err := customerservice.New(repository, ids, clock)
		if err != nil {
			t.Fatalf("customerservice.New() error = %v", err)
		}
		services = append(services, service)
	}
	return services
}

func validReceiveCommand(externalMessageID, content string) customerservice.ReceiveMessageCommand {
	return customerservice.ReceiveMessageCommand{
		ConversationID:    "conversation-1",
		CustomerID:        "customer-1",
		Channel:           "web",
		ExternalMessageID: externalMessageID,
		Content:           content,
	}
}

func newConversation(t *testing.T, id, customerID, channel string) *conversation.Conversation {
	t.Helper()
	current, err := conversation.New(conversation.CreateParams{
		ID:         id,
		CustomerID: customerID,
		Channel:    channel,
		CreatedAt:  integrationTime,
	})
	if err != nil {
		t.Fatalf("conversation.New() error = %v", err)
	}
	return current
}

func newCustomerMessage(
	t *testing.T,
	id string,
	conversationID string,
	externalMessageID string,
	content string,
) conversation.Message {
	t.Helper()
	message, err := conversation.NewMessage(conversation.MessageParams{
		ID:                id,
		ConversationID:    conversationID,
		ExternalMessageID: externalMessageID,
		Sender:            conversation.SenderCustomer,
		SenderID:          "customer-1",
		Content:           content,
		CreatedAt:         integrationTime,
	})
	if err != nil {
		t.Fatalf("conversation.NewMessage() error = %v", err)
	}
	return message
}

func newBotReply(
	t *testing.T,
	id string,
	conversationID string,
	triggerMessageID string,
	content string,
) conversation.Message {
	t.Helper()
	message, err := conversation.NewMessage(conversation.MessageParams{
		ID:               id,
		ConversationID:   conversationID,
		ReplyToMessageID: triggerMessageID,
		Sender:           conversation.SenderBot,
		Content:          content,
		CreatedAt:        integrationTime,
	})
	if err != nil {
		t.Fatalf("conversation.NewMessage() error = %v", err)
	}
	return message
}

func assertConversationEqual(
	t *testing.T,
	actual *conversation.Conversation,
	expected *conversation.Conversation,
) {
	t.Helper()
	if actual.ID() != expected.ID() ||
		actual.CustomerID() != expected.CustomerID() ||
		actual.Channel() != expected.Channel() ||
		actual.Status() != expected.Status() ||
		actual.AgentID() != expected.AgentID() ||
		actual.Revision() != expected.Revision() ||
		!actual.CreatedAt().Equal(expected.CreatedAt()) ||
		!actual.UpdatedAt().Equal(expected.UpdatedAt()) {
		t.Fatalf("conversation = %#v, want %#v", actual, expected)
	}
}

func messageIDs(messages []conversation.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID())
	}
	return ids
}

type synchronizedReplyGenerator struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int64
}

func newSynchronizedReplyGenerator() *synchronizedReplyGenerator {
	return &synchronizedReplyGenerator{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
}

func (g *synchronizedReplyGenerator) Generate(
	ctx context.Context,
	_ customerservice.ReplyRequest,
) (customerservice.Reply, error) {
	g.calls.Add(1)
	select {
	case g.entered <- struct{}{}:
	case <-ctx.Done():
		return customerservice.Reply{}, ctx.Err()
	}
	select {
	case <-g.release:
		return customerservice.Reply{Content: "synchronized answer"}, nil
	case <-ctx.Done():
		return customerservice.Reply{}, ctx.Err()
	}
}
