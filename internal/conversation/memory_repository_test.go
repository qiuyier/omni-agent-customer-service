package conversation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func TestMemoryRepositoryTransactionCommit(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	current := newConversation(t)
	message := newCustomerMessage(t, current.ID(), "message-1", "web-1")

	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(current); err != nil {
			return err
		}
		return tx.InsertMessage(current.Channel(), message)
	})
	if err != nil {
		t.Fatalf("WithinTx() error = %v", err)
	}

	stored, err := repository.FindConversation(context.Background(), current.ID())
	if err != nil {
		t.Fatalf("FindConversation() error = %v", err)
	}
	if stored.ID() != current.ID() {
		t.Fatalf("stored ID = %q, want %q", stored.ID(), current.ID())
	}
	messages, err := repository.ListMessages(context.Background(), current.ID())
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].ID() != message.ID() {
		t.Fatalf("stored messages = %#v, want message %q", messages, message.ID())
	}
}

func TestMemoryRepositoryTransactionRollback(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	current := newConversation(t)
	message := newCustomerMessage(t, current.ID(), "message-1", "web-1")
	wantErr := errors.New("force rollback")

	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(current); err != nil {
			return err
		}
		if err := tx.InsertMessage(current.Channel(), message); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithinTx() error = %v, want %v", err, wantErr)
	}

	_, err = repository.FindConversation(context.Background(), current.ID())
	if !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatalf("FindConversation() error = %v, want ErrConversationNotFound", err)
	}
	_, err = repository.FindMessageByExternalID(context.Background(), "web", "web-1")
	if !errors.Is(err, conversation.ErrMessageNotFound) {
		t.Fatalf("FindMessageByExternalID() error = %v, want ErrMessageNotFound", err)
	}
}

func TestMemoryRepositoryReturnsDetachedConversation(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	current := newConversation(t)
	insertConversation(t, repository, current)

	loaded, err := repository.FindConversation(context.Background(), current.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.RequestHandoff(baseTime.Add(1)); err != nil {
		t.Fatal(err)
	}

	unchanged, err := repository.FindConversation(context.Background(), current.ID())
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status() != conversation.StatusBotActive {
		t.Fatalf("repository state changed through detached copy: %q", unchanged.Status())
	}

	err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		stored, findErr := tx.FindConversation(current.ID())
		if findErr != nil {
			return findErr
		}
		if transitionErr := stored.RequestHandoff(baseTime.Add(2)); transitionErr != nil {
			return transitionErr
		}
		return tx.UpdateConversation(stored)
	})
	if err != nil {
		t.Fatalf("WithinTx() update error = %v", err)
	}

	updated, err := repository.FindConversation(context.Background(), current.ID())
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status() != conversation.StatusWaitingHuman {
		t.Fatalf("updated status = %q, want waiting_human", updated.Status())
	}
}

func TestMemoryRepositoryExternalIDIsChannelScoped(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	webConversation := newConversation(t)
	mobileConversation, err := conversation.New(conversation.CreateParams{
		ID:         "conv-mobile",
		CustomerID: "customer-2",
		Channel:    "mobile",
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	webMessage := newCustomerMessage(t, webConversation.ID(), "message-web", "same-external-id")
	mobileMessage, err := conversation.NewMessage(conversation.MessageParams{
		ID:                "message-mobile",
		ConversationID:    mobileConversation.ID(),
		ExternalMessageID: "same-external-id",
		Sender:            conversation.SenderCustomer,
		SenderID:          mobileConversation.CustomerID(),
		Content:           "hello from mobile",
		CreatedAt:         baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		if insertErr := tx.InsertConversation(webConversation); insertErr != nil {
			return insertErr
		}
		if insertErr := tx.InsertConversation(mobileConversation); insertErr != nil {
			return insertErr
		}
		if insertErr := tx.InsertMessage("web", webMessage); insertErr != nil {
			return insertErr
		}
		return tx.InsertMessage("mobile", mobileMessage)
	})
	if err != nil {
		t.Fatalf("WithinTx() error = %v", err)
	}

	storedWeb, err := repository.FindMessageByExternalID(
		context.Background(),
		"web",
		"same-external-id",
	)
	if err != nil {
		t.Fatal(err)
	}
	storedMobile, err := repository.FindMessageByExternalID(
		context.Background(),
		"mobile",
		"same-external-id",
	)
	if err != nil {
		t.Fatal(err)
	}
	if storedWeb.ID() == storedMobile.ID() {
		t.Fatalf("channel-scoped lookups returned the same message %q", storedWeb.ID())
	}
}

func insertConversation(
	t *testing.T,
	repository *conversation.MemoryRepository,
	current *conversation.Conversation,
) {
	t.Helper()
	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		return tx.InsertConversation(current)
	})
	if err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
}

func newCustomerMessage(
	t *testing.T,
	conversationID string,
	messageID string,
	externalMessageID string,
) conversation.Message {
	t.Helper()
	message, err := conversation.NewMessage(conversation.MessageParams{
		ID:                messageID,
		ConversationID:    conversationID,
		ExternalMessageID: externalMessageID,
		Sender:            conversation.SenderCustomer,
		SenderID:          "customer-1",
		Content:           "hello",
		CreatedAt:         baseTime,
	})
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	return message
}
