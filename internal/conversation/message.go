package conversation

import (
	"fmt"
	"strings"
	"time"
)

// Sender identifies the actor that produced a message.
type Sender string

const (
	SenderCustomer   Sender = "customer"
	SenderBot        Sender = "bot"
	SenderHumanAgent Sender = "human_agent"
	SenderSystem     Sender = "system"
	SenderTool       Sender = "tool"
)

// Valid reports whether sender is a known sender type.
func (s Sender) Valid() bool {
	switch s {
	case SenderCustomer, SenderBot, SenderHumanAgent, SenderSystem, SenderTool:
		return true
	default:
		return false
	}
}

// MessageParams contains the data required to create an immutable text
// message. ExternalMessageID is optional for messages produced internally.
type MessageParams struct {
	ID                string
	ConversationID    string
	ExternalMessageID string
	Sender            Sender
	SenderID          string
	Content           string
	CreatedAt         time.Time
}

// Message is an immutable text message in a conversation.
type Message struct {
	id                string
	conversationID    string
	externalMessageID string
	sender            Sender
	senderID          string
	content           string
	createdAt         time.Time
}

// NewMessage validates and creates a message.
func NewMessage(params MessageParams) (Message, error) {
	id, err := requiredString("id", params.ID)
	if err != nil {
		return Message{}, err
	}
	conversationID, err := requiredString("conversation_id", params.ConversationID)
	if err != nil {
		return Message{}, err
	}
	if !params.Sender.Valid() {
		return Message{}, invalidArgument("sender", fmt.Sprintf("%q is unknown", params.Sender))
	}
	senderID := strings.TrimSpace(params.SenderID)
	if (params.Sender == SenderCustomer || params.Sender == SenderHumanAgent) && senderID == "" {
		return Message{}, invalidArgument("sender_id", "is required for customer and human_agent messages")
	}
	if strings.TrimSpace(params.Content) == "" {
		return Message{}, invalidArgument("content", "is required")
	}
	createdAt, err := requiredTime("created_at", params.CreatedAt)
	if err != nil {
		return Message{}, err
	}

	return Message{
		id:                id,
		conversationID:    conversationID,
		externalMessageID: strings.TrimSpace(params.ExternalMessageID),
		sender:            params.Sender,
		senderID:          senderID,
		content:           params.Content,
		createdAt:         createdAt,
	}, nil
}

// ID returns the message identifier.
func (m Message) ID() string { return m.id }

// ConversationID returns the owning conversation identifier.
func (m Message) ConversationID() string { return m.conversationID }

// ExternalMessageID returns the source-channel identifier used for idempotency.
func (m Message) ExternalMessageID() string { return m.externalMessageID }

// Sender returns the actor type.
func (m Message) Sender() Sender { return m.sender }

// SenderID returns the concrete customer or agent identifier when applicable.
func (m Message) SenderID() string { return m.senderID }

// Content returns the original text content.
func (m Message) Content() string { return m.content }

// CreatedAt returns the creation time in UTC.
func (m Message) CreatedAt() time.Time { return m.createdAt }
