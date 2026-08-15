package conversation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func TestMemoryRepositoryIndexesOneReplyPerTrigger(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	current := newConversation(t)
	trigger := newCustomerMessage(t, current.ID(), "customer-message-1", "web-1")
	reply := newBotReply(t, current.ID(), "bot-message-1", trigger.ID())

	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(current); err != nil {
			return err
		}
		if err := tx.InsertMessage(current.Channel(), trigger); err != nil {
			return err
		}
		return tx.InsertMessage(current.Channel(), reply)
	})
	if err != nil {
		t.Fatalf("WithinTx() error = %v", err)
	}

	stored, err := repository.FindReplyByTriggerMessageID(context.Background(), trigger.ID())
	if err != nil {
		t.Fatalf("FindReplyByTriggerMessageID() error = %v", err)
	}
	if stored.ID() != reply.ID() || stored.ReplyToMessageID() != trigger.ID() {
		t.Fatalf("stored reply = ID %q trigger %q", stored.ID(), stored.ReplyToMessageID())
	}

	second := newBotReply(t, current.ID(), "bot-message-2", trigger.ID())
	err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		return tx.InsertMessage(current.Channel(), second)
	})
	if !errors.Is(err, conversation.ErrReplyAlreadyExists) {
		t.Fatalf("second reply error = %v, want ErrReplyAlreadyExists", err)
	}
	if messages, listErr := repository.ListMessages(context.Background(), current.ID()); listErr != nil || len(messages) != 2 {
		t.Fatalf("messages after duplicate = %d, error %v", len(messages), listErr)
	}
}

func TestMemoryRepositoryRejectsInvalidReplyTargets(t *testing.T) {
	t.Run("unknown target", func(t *testing.T) {
		repository := conversation.NewMemoryRepository()
		current := newConversation(t)
		insertConversation(t, repository, current)
		reply := newBotReply(t, current.ID(), "bot-message-1", "missing-message")

		err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
			return tx.InsertMessage(current.Channel(), reply)
		})
		if !errors.Is(err, conversation.ErrMessageNotFound) {
			t.Fatalf("InsertMessage() error = %v, want ErrMessageNotFound", err)
		}
	})

	t.Run("target in another conversation", func(t *testing.T) {
		repository := conversation.NewMemoryRepository()
		first := newConversation(t)
		second, err := conversation.New(conversation.CreateParams{
			ID:         "conv-2",
			CustomerID: "customer-2",
			Channel:    "web",
			CreatedAt:  baseTime,
		})
		if err != nil {
			t.Fatal(err)
		}
		trigger := newCustomerMessage(t, first.ID(), "customer-message-1", "web-1")
		reply := newBotReply(t, second.ID(), "bot-message-1", trigger.ID())
		err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
			if insertErr := tx.InsertConversation(first); insertErr != nil {
				return insertErr
			}
			if insertErr := tx.InsertConversation(second); insertErr != nil {
				return insertErr
			}
			if insertErr := tx.InsertMessage(first.Channel(), trigger); insertErr != nil {
				return insertErr
			}
			return tx.InsertMessage(second.Channel(), reply)
		})
		if !errors.Is(err, conversation.ErrInvalidArgument) {
			t.Fatalf("InsertMessage() error = %v, want ErrInvalidArgument", err)
		}
	})

	t.Run("target is not customer message", func(t *testing.T) {
		repository := conversation.NewMemoryRepository()
		current := newConversation(t)
		botTarget, err := conversation.NewMessage(conversation.MessageParams{
			ID:             "bot-target",
			ConversationID: current.ID(),
			Sender:         conversation.SenderBot,
			Content:        "proactive greeting",
			CreatedAt:      baseTime,
		})
		if err != nil {
			t.Fatal(err)
		}
		reply := newBotReply(t, current.ID(), "bot-message-1", botTarget.ID())
		err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
			if insertErr := tx.InsertConversation(current); insertErr != nil {
				return insertErr
			}
			if insertErr := tx.InsertMessage(current.Channel(), botTarget); insertErr != nil {
				return insertErr
			}
			return tx.InsertMessage(current.Channel(), reply)
		})
		if !errors.Is(err, conversation.ErrInvalidArgument) {
			t.Fatalf("InsertMessage() error = %v, want ErrInvalidArgument", err)
		}
	})
}

func TestMemoryRepositoryRollsBackReplyIndex(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	current := newConversation(t)
	insertConversation(t, repository, current)
	trigger := newCustomerMessage(t, current.ID(), "customer-message-1", "web-1")
	reply := newBotReply(t, current.ID(), "bot-message-1", trigger.ID())
	wantErr := errors.New("rollback reply")

	err := repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		if err := tx.InsertMessage(current.Channel(), trigger); err != nil {
			return err
		}
		if err := tx.InsertMessage(current.Channel(), reply); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithinTx() error = %v, want rollback error", err)
	}
	_, err = repository.FindReplyByTriggerMessageID(context.Background(), trigger.ID())
	if !errors.Is(err, conversation.ErrReplyNotFound) {
		t.Fatalf("FindReplyByTriggerMessageID() error = %v, want ErrReplyNotFound", err)
	}
}

func TestFailedReplyInsertDoesNotPolluteTransactionIndexes(t *testing.T) {
	repository := conversation.NewMemoryRepository()
	current := newConversation(t)
	insertConversation(t, repository, current)
	invalidReply, err := conversation.NewMessage(conversation.MessageParams{
		ID:                "invalid-reply",
		ConversationID:    current.ID(),
		ExternalMessageID: "reusable-external-id",
		ReplyToMessageID:  "missing-trigger",
		Sender:            conversation.SenderBot,
		Content:           "invalid",
		CreatedAt:         baseTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	validMessage := newCustomerMessage(t, current.ID(), "customer-message-1", "reusable-external-id")

	err = repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		insertErr := tx.InsertMessage(current.Channel(), invalidReply)
		if !errors.Is(insertErr, conversation.ErrMessageNotFound) {
			return insertErr
		}
		if _, findErr := tx.FindMessageByExternalID(current.Channel(), "reusable-external-id"); !errors.Is(findErr, conversation.ErrMessageNotFound) {
			return findErr
		}
		return tx.InsertMessage(current.Channel(), validMessage)
	})
	if err != nil {
		t.Fatalf("WithinTx() error = %v", err)
	}
	stored, err := repository.FindMessageByExternalID(
		context.Background(),
		current.Channel(),
		"reusable-external-id",
	)
	if err != nil || stored.ID() != validMessage.ID() {
		t.Fatalf("stored message = %q, error %v", stored.ID(), err)
	}
}

func newBotReply(
	t *testing.T,
	conversationID string,
	messageID string,
	triggerMessageID string,
) conversation.Message {
	t.Helper()
	message, err := conversation.NewMessage(conversation.MessageParams{
		ID:               messageID,
		ConversationID:   conversationID,
		ReplyToMessageID: triggerMessageID,
		Sender:           conversation.SenderBot,
		Content:          "bot reply",
		CreatedAt:        baseTime,
	})
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	return message
}
