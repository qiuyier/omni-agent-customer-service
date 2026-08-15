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
// ReplyToMessageID links a bot reply to the customer message that triggered it.
type MessageParams struct {
	ID                string
	ConversationID    string
	ExternalMessageID string
	ReplyToMessageID  string
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
	replyToMessageID  string
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
	replyToMessageID := strings.TrimSpace(params.ReplyToMessageID)
	if params.ReplyToMessageID != "" && replyToMessageID == "" {
		return Message{}, invalidArgument("reply_to_message_id", "must not be blank")
	}
	if replyToMessageID != "" {
		if params.Sender != SenderBot {
			return Message{}, invalidArgument("reply_to_message_id", "is only allowed for bot messages")
		}
		if replyToMessageID == id {
			return Message{}, invalidArgument("reply_to_message_id", "must not reference the message itself")
		}
	}
	createdAt, err := requiredTime("created_at", params.CreatedAt)
	if err != nil {
		return Message{}, err
	}

	return Message{
		id:                id,
		conversationID:    conversationID,
		externalMessageID: strings.TrimSpace(params.ExternalMessageID),
		replyToMessageID:  replyToMessageID,
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

// ReplyToMessageID returns the customer message that triggered this bot reply.
func (m Message) ReplyToMessageID() string { return m.replyToMessageID }

// Sender returns the actor type.
func (m Message) Sender() Sender { return m.sender }

// SenderID returns the concrete customer or agent identifier when applicable.
func (m Message) SenderID() string { return m.senderID }

// Content returns the original text content.
func (m Message) Content() string { return m.content }

// CreatedAt returns the creation time in UTC.
func (m Message) CreatedAt() time.Time { return m.createdAt }
