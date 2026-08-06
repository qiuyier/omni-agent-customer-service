package conversation_test

import (
	"errors"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

var baseTime = time.Date(2026, time.August, 6, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))

func TestNewConversation(t *testing.T) {
	created, err := conversation.New(conversation.CreateParams{
		ID:         "conv-1",
		CustomerID: "customer-1",
		Channel:    "web",
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if created.Status() != conversation.StatusBotActive {
		t.Fatalf("Status() = %q, want %q", created.Status(), conversation.StatusBotActive)
	}
	if created.AgentID() != "" {
		t.Fatalf("AgentID() = %q, want empty", created.AgentID())
	}
	if created.CreatedAt().Location() != time.UTC || created.UpdatedAt() != created.CreatedAt() {
		t.Fatalf("timestamps were not normalized: created=%v updated=%v", created.CreatedAt(), created.UpdatedAt())
	}
}

func TestNewConversationValidation(t *testing.T) {
	tests := []struct {
		name   string
		params conversation.CreateParams
	}{
		{name: "missing id", params: conversation.CreateParams{CustomerID: "customer", Channel: "web", CreatedAt: baseTime}},
		{name: "missing customer", params: conversation.CreateParams{ID: "conv", Channel: "web", CreatedAt: baseTime}},
		{name: "missing channel", params: conversation.CreateParams{ID: "conv", CustomerID: "customer", CreatedAt: baseTime}},
		{name: "missing timestamp", params: conversation.CreateParams{ID: "conv", CustomerID: "customer", Channel: "web"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := conversation.New(test.params)
			if !errors.Is(err, conversation.ErrInvalidArgument) {
				t.Fatalf("New() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestHandoffLifecycle(t *testing.T) {
	current := newConversation(t)

	if err := current.RequestHandoff(baseTime.Add(time.Minute)); err != nil {
		t.Fatalf("RequestHandoff() error = %v", err)
	}
	if current.Status() != conversation.StatusWaitingHuman {
		t.Fatalf("status after handoff request = %q", current.Status())
	}

	if err := current.CancelHandoff(baseTime.Add(2 * time.Minute)); err != nil {
		t.Fatalf("CancelHandoff() error = %v", err)
	}
	if current.Status() != conversation.StatusBotActive {
		t.Fatalf("status after cancellation = %q", current.Status())
	}

	if err := current.RequestHandoff(baseTime.Add(3 * time.Minute)); err != nil {
		t.Fatalf("second RequestHandoff() error = %v", err)
	}
	if err := current.AssignAgent("agent-7", baseTime.Add(4*time.Minute)); err != nil {
		t.Fatalf("AssignAgent() error = %v", err)
	}
	if current.Status() != conversation.StatusHumanActive || current.AgentID() != "agent-7" {
		t.Fatalf("assigned conversation = status %q, agent %q", current.Status(), current.AgentID())
	}

	if err := current.Close(baseTime.Add(5 * time.Minute)); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if current.Status() != conversation.StatusClosed {
		t.Fatalf("status after close = %q", current.Status())
	}
	if err := current.Close(baseTime.Add(6 * time.Minute)); !errors.Is(err, conversation.ErrConversationClosed) {
		t.Fatalf("second Close() error = %v, want ErrConversationClosed", err)
	}
}

func TestInvalidTransitions(t *testing.T) {
	t.Run("assign before queueing", func(t *testing.T) {
		current := newConversation(t)
		err := current.AssignAgent("agent-1", baseTime.Add(time.Minute))
		if !errors.Is(err, conversation.ErrInvalidTransition) {
			t.Fatalf("AssignAgent() error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("request twice", func(t *testing.T) {
		current := newConversation(t)
		if err := current.RequestHandoff(baseTime.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		err := current.RequestHandoff(baseTime.Add(2 * time.Minute))
		if !errors.Is(err, conversation.ErrInvalidTransition) {
			t.Fatalf("second RequestHandoff() error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("transition after close", func(t *testing.T) {
		current := newConversation(t)
		if err := current.Close(baseTime.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		err := current.RequestHandoff(baseTime.Add(2 * time.Minute))
		if !errors.Is(err, conversation.ErrConversationClosed) {
			t.Fatalf("RequestHandoff() error = %v, want ErrConversationClosed", err)
		}
	})

	t.Run("timestamp goes backwards", func(t *testing.T) {
		current := newConversation(t)
		err := current.RequestHandoff(baseTime.Add(-time.Minute))
		if !errors.Is(err, conversation.ErrInvalidArgument) {
			t.Fatalf("RequestHandoff() error = %v, want ErrInvalidArgument", err)
		}
		if current.Status() != conversation.StatusBotActive {
			t.Fatalf("failed transition mutated status to %q", current.Status())
		}
	})
}

func TestMessagePermissionByStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  conversation.Status
		sender  conversation.Sender
		allowed bool
	}{
		{name: "customer while bot active", status: conversation.StatusBotActive, sender: conversation.SenderCustomer, allowed: true},
		{name: "bot while bot active", status: conversation.StatusBotActive, sender: conversation.SenderBot, allowed: true},
		{name: "tool while bot active", status: conversation.StatusBotActive, sender: conversation.SenderTool, allowed: true},
		{name: "human while bot active", status: conversation.StatusBotActive, sender: conversation.SenderHumanAgent, allowed: false},
		{name: "customer while waiting", status: conversation.StatusWaitingHuman, sender: conversation.SenderCustomer, allowed: true},
		{name: "bot while waiting", status: conversation.StatusWaitingHuman, sender: conversation.SenderBot, allowed: false},
		{name: "human while waiting", status: conversation.StatusWaitingHuman, sender: conversation.SenderHumanAgent, allowed: false},
		{name: "human while active", status: conversation.StatusHumanActive, sender: conversation.SenderHumanAgent, allowed: true},
		{name: "bot while human active", status: conversation.StatusHumanActive, sender: conversation.SenderBot, allowed: false},
		{name: "customer while human active", status: conversation.StatusHumanActive, sender: conversation.SenderCustomer, allowed: true},
		{name: "customer after close", status: conversation.StatusClosed, sender: conversation.SenderCustomer, allowed: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := conversationWithStatus(t, test.status)
			err := current.CanAcceptMessage(test.sender)
			if test.allowed && err != nil {
				t.Fatalf("CanAcceptMessage() error = %v", err)
			}
			if !test.allowed && err == nil {
				t.Fatal("CanAcceptMessage() error = nil")
			}
			if test.status == conversation.StatusClosed && !errors.Is(err, conversation.ErrConversationClosed) {
				t.Fatalf("closed error = %v, want ErrConversationClosed", err)
			}
		})
	}
}

func newConversation(t *testing.T) *conversation.Conversation {
	t.Helper()
	current, err := conversation.New(conversation.CreateParams{
		ID:         "conv-1",
		CustomerID: "customer-1",
		Channel:    "web",
		CreatedAt:  baseTime,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return current
}

func conversationWithStatus(t *testing.T, status conversation.Status) *conversation.Conversation {
	t.Helper()
	current := newConversation(t)
	switch status {
	case conversation.StatusBotActive:
	case conversation.StatusWaitingHuman:
		if err := current.RequestHandoff(baseTime.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	case conversation.StatusHumanActive:
		if err := current.RequestHandoff(baseTime.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := current.AssignAgent("agent-1", baseTime.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	case conversation.StatusClosed:
		if err := current.Close(baseTime.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported test status %q", status)
	}
	return current
}
