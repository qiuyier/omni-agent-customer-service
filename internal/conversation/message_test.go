package conversation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func TestNewMessage(t *testing.T) {
	message, err := conversation.NewMessage(conversation.MessageParams{
		ID:                "message-1",
		ConversationID:    "conversation-1",
		ExternalMessageID: "web-42",
		Sender:            conversation.SenderCustomer,
		SenderID:          "customer-1",
		Content:           "  I need help with my order.  ",
		CreatedAt:         baseTime,
	})
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}

	if message.ExternalMessageID() != "web-42" {
		t.Fatalf("ExternalMessageID() = %q", message.ExternalMessageID())
	}
	if message.Content() != "  I need help with my order.  " {
		t.Fatalf("Content() did not preserve original text: %q", message.Content())
	}
	if message.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt() location = %v, want UTC", message.CreatedAt().Location())
	}
}

func TestNewMessageValidation(t *testing.T) {
	valid := conversation.MessageParams{
		ID:             "message-1",
		ConversationID: "conversation-1",
		Sender:         conversation.SenderCustomer,
		SenderID:       "customer-1",
		Content:        "hello",
		CreatedAt:      baseTime,
	}

	tests := []struct {
		name   string
		mutate func(*conversation.MessageParams)
	}{
		{name: "missing id", mutate: func(p *conversation.MessageParams) { p.ID = "" }},
		{name: "missing conversation", mutate: func(p *conversation.MessageParams) { p.ConversationID = "" }},
		{name: "unknown sender", mutate: func(p *conversation.MessageParams) { p.Sender = conversation.Sender("unknown") }},
		{name: "customer without sender id", mutate: func(p *conversation.MessageParams) { p.SenderID = "" }},
		{name: "human without sender id", mutate: func(p *conversation.MessageParams) {
			p.Sender = conversation.SenderHumanAgent
			p.SenderID = ""
		}},
		{name: "empty content", mutate: func(p *conversation.MessageParams) { p.Content = "  " }},
		{name: "missing timestamp", mutate: func(p *conversation.MessageParams) { p.CreatedAt = time.Time{} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := valid
			test.mutate(&params)
			_, err := conversation.NewMessage(params)
			if !errors.Is(err, conversation.ErrInvalidArgument) {
				t.Fatalf("NewMessage() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestInternalMessageDoesNotRequireSenderID(t *testing.T) {
	for _, sender := range []conversation.Sender{
		conversation.SenderBot,
		conversation.SenderSystem,
		conversation.SenderTool,
	} {
		t.Run(string(sender), func(t *testing.T) {
			_, err := conversation.NewMessage(conversation.MessageParams{
				ID:             "message-1",
				ConversationID: "conversation-1",
				Sender:         sender,
				Content:        "internal message",
				CreatedAt:      baseTime,
			})
			if err != nil {
				t.Fatalf("NewMessage() error = %v", err)
			}
		})
	}
}
