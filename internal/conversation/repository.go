package conversation

import (
	"context"
	"errors"
)

var (
	// ErrConversationNotFound indicates that a conversation ID is unknown.
	ErrConversationNotFound = errors.New("conversation: conversation not found")

	// ErrConversationAlreadyExists indicates that a conversation ID is already
	// in use.
	ErrConversationAlreadyExists = errors.New("conversation: conversation already exists")

	// ErrMessageNotFound indicates that a message lookup found no result.
	ErrMessageNotFound = errors.New("conversation: message not found")

	// ErrMessageAlreadyExists indicates that an internal message ID is already
	// in use.
	ErrMessageAlreadyExists = errors.New("conversation: message already exists")

	// ErrDuplicateExternalMessage indicates that a source channel retried a
	// message whose external ID has already been stored.
	ErrDuplicateExternalMessage = errors.New("conversation: duplicate external message")
)

// Repository is the persistence port required by the customer-service
// application layer. All writes happen through WithinTx so creating a
// conversation and appending its first message cannot be partially persisted.
type Repository interface {
	FindConversation(ctx context.Context, id string) (*Conversation, error)
	FindMessageByExternalID(ctx context.Context, channel, externalMessageID string) (Message, error)
	ListMessages(ctx context.Context, conversationID string) ([]Message, error)
	WithinTx(ctx context.Context, fn func(Transaction) error) error
}

// Transaction exposes the operations allowed inside one atomic repository
// transaction. Conversation changes still have to go through domain methods
// before UpdateConversation is called.
type Transaction interface {
	FindConversation(id string) (*Conversation, error)
	InsertConversation(current *Conversation) error
	UpdateConversation(current *Conversation) error
	FindMessageByExternalID(channel, externalMessageID string) (Message, error)
	InsertMessage(channel string, message Message) error
}
