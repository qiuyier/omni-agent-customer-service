package customerservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

// NextAction tells the caller which subsystem should handle the accepted
// customer message next.
type NextAction string

const (
	NextActionGenerateBotReply NextAction = "generate_bot_reply"
	NextActionWaitForHuman     NextAction = "wait_for_human"
	NextActionDeliverToHuman   NextAction = "deliver_to_human"
	NextActionNone             NextAction = "none"
)

// Clock supplies server time without making tests depend on the wall clock.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now returns the function's current time.
func (f ClockFunc) Now() time.Time { return f() }

// MessageIDGenerator creates internal, channel-independent message IDs. Its
// implementation must be local and non-blocking because generation occurs just
// before the repository transaction.
type MessageIDGenerator interface {
	NewMessageID() (string, error)
}

// MessageIDGeneratorFunc adapts a function to MessageIDGenerator.
type MessageIDGeneratorFunc func() (string, error)

// NewMessageID calls the underlying function.
func (f MessageIDGeneratorFunc) NewMessageID() (string, error) { return f() }

// Service coordinates customer-service application use cases.
type Service struct {
	repository conversation.Repository
	ids        MessageIDGenerator
	clock      Clock
	replies    ReplyGenerator
}

// New creates a customer-service application service.
func New(
	repository conversation.Repository,
	ids MessageIDGenerator,
	clock Clock,
	options ...Option,
) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("%w: repository is nil", ErrInvalidDependency)
	}
	if ids == nil {
		return nil, fmt.Errorf("%w: message ID generator is nil", ErrInvalidDependency)
	}
	if clock == nil {
		return nil, fmt.Errorf("%w: clock is nil", ErrInvalidDependency)
	}
	service := &Service{repository: repository, ids: ids, clock: clock}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: option %d is nil", ErrInvalidDependency, index)
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// ReceiveMessageCommand contains one inbound customer message. Sender is not a
// field on purpose: this public use case always creates a customer message and
// cannot be used to impersonate a bot, tool, or human agent.
type ReceiveMessageCommand struct {
	ConversationID    string
	CustomerID        string
	Channel           string
	ExternalMessageID string
	Content           string
}

// ReceiveMessageResult reports what was stored and what should happen next.
// Duplicate deliveries always return NextActionNone so a channel retry cannot
// trigger a second model call or human notification.
type ReceiveMessageResult struct {
	ConversationID string
	MessageID      string
	Status         conversation.Status
	NextAction     NextAction
	Duplicate      bool
}

// ReceiveMessage idempotently accepts one inbound customer message. Creating a
// missing conversation and appending its first message happen in one repository
// transaction.
func (s *Service) ReceiveMessage(
	ctx context.Context,
	command ReceiveMessageCommand,
) (ReceiveMessageResult, error) {
	if ctx == nil {
		return ReceiveMessageResult{}, invalidInput("context", "is nil")
	}
	if err := ctx.Err(); err != nil {
		return ReceiveMessageResult{}, err
	}

	normalized, err := normalizeReceiveMessageCommand(command)
	if err != nil {
		return ReceiveMessageResult{}, err
	}

	// This fast path avoids consuming an internal ID for the common webhook retry
	// case. The transaction repeats the lookup to close the concurrency window.
	existing, err := s.repository.FindMessageByExternalID(
		ctx,
		normalized.Channel,
		normalized.ExternalMessageID,
	)
	if err == nil {
		current, findErr := s.repository.FindConversation(ctx, existing.ConversationID())
		if findErr != nil {
			return ReceiveMessageResult{}, findErr
		}
		return duplicateResult(normalized, current, existing)
	}
	if !errors.Is(err, conversation.ErrMessageNotFound) {
		return ReceiveMessageResult{}, err
	}

	messageID, err := s.ids.NewMessageID()
	if err != nil {
		return ReceiveMessageResult{}, fmt.Errorf("generate message ID: %w", err)
	}
	receivedAt := s.clock.Now()

	var result ReceiveMessageResult
	err = s.repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		// Another goroutine may have committed the same external message between
		// the fast lookup and this transaction.
		existing, findErr := tx.FindMessageByExternalID(
			normalized.Channel,
			normalized.ExternalMessageID,
		)
		if findErr == nil {
			current, conversationErr := tx.FindConversation(existing.ConversationID())
			if conversationErr != nil {
				return conversationErr
			}
			duplicate, duplicateErr := duplicateResult(normalized, current, existing)
			if duplicateErr != nil {
				return duplicateErr
			}
			result = duplicate
			return nil
		}
		if !errors.Is(findErr, conversation.ErrMessageNotFound) {
			return findErr
		}

		current, findErr := tx.FindConversation(normalized.ConversationID)
		switch {
		case findErr == nil:
			if matchErr := validateConversationIdentity(normalized, current); matchErr != nil {
				return matchErr
			}
		case errors.Is(findErr, conversation.ErrConversationNotFound):
			current, findErr = conversation.New(conversation.CreateParams{
				ID:         normalized.ConversationID,
				CustomerID: normalized.CustomerID,
				Channel:    normalized.Channel,
				CreatedAt:  receivedAt,
			})
			if findErr != nil {
				return findErr
			}
			if insertErr := tx.InsertConversation(current); insertErr != nil {
				return insertErr
			}
		default:
			return findErr
		}

		if permissionErr := current.CanAcceptMessage(conversation.SenderCustomer); permissionErr != nil {
			return permissionErr
		}
		message, messageErr := conversation.NewMessage(conversation.MessageParams{
			ID:                messageID,
			ConversationID:    current.ID(),
			ExternalMessageID: normalized.ExternalMessageID,
			Sender:            conversation.SenderCustomer,
			SenderID:          normalized.CustomerID,
			Content:           normalized.Content,
			CreatedAt:         receivedAt,
		})
		if messageErr != nil {
			return messageErr
		}
		if insertErr := tx.InsertMessage(normalized.Channel, message); insertErr != nil {
			return insertErr
		}

		result = ReceiveMessageResult{
			ConversationID: current.ID(),
			MessageID:      message.ID(),
			Status:         current.Status(),
			NextAction:     nextActionFor(current.Status()),
		}
		return nil
	})
	if err != nil {
		return ReceiveMessageResult{}, err
	}
	return result, nil
}

func duplicateResult(
	command ReceiveMessageCommand,
	current *conversation.Conversation,
	message conversation.Message,
) (ReceiveMessageResult, error) {
	if current == nil ||
		current.ID() != command.ConversationID ||
		current.CustomerID() != command.CustomerID ||
		current.Channel() != command.Channel ||
		message.ConversationID() != command.ConversationID ||
		message.Sender() != conversation.SenderCustomer ||
		message.SenderID() != command.CustomerID ||
		message.Content() != command.Content {
		return ReceiveMessageResult{}, fmt.Errorf(
			"%w: external message ID %q was reused with different data",
			ErrIdempotencyConflict,
			command.ExternalMessageID,
		)
	}
	return ReceiveMessageResult{
		ConversationID: current.ID(),
		MessageID:      message.ID(),
		Status:         current.Status(),
		NextAction:     NextActionNone,
		Duplicate:      true,
	}, nil
}

func validateConversationIdentity(
	command ReceiveMessageCommand,
	current *conversation.Conversation,
) error {
	if current.CustomerID() != command.CustomerID {
		return fmt.Errorf(
			"%w: conversation %q belongs to another customer",
			ErrConversationMismatch,
			command.ConversationID,
		)
	}
	if current.Channel() != command.Channel {
		return fmt.Errorf(
			"%w: conversation %q belongs to another channel",
			ErrConversationMismatch,
			command.ConversationID,
		)
	}
	return nil
}

func nextActionFor(status conversation.Status) NextAction {
	switch status {
	case conversation.StatusBotActive:
		return NextActionGenerateBotReply
	case conversation.StatusWaitingHuman:
		return NextActionWaitForHuman
	case conversation.StatusHumanActive:
		return NextActionDeliverToHuman
	default:
		return NextActionNone
	}
}

func normalizeReceiveMessageCommand(command ReceiveMessageCommand) (ReceiveMessageCommand, error) {
	command.ConversationID = strings.TrimSpace(command.ConversationID)
	if command.ConversationID == "" {
		return ReceiveMessageCommand{}, invalidInput("conversation_id", "is required")
	}
	command.CustomerID = strings.TrimSpace(command.CustomerID)
	if command.CustomerID == "" {
		return ReceiveMessageCommand{}, invalidInput("customer_id", "is required")
	}
	command.Channel = strings.TrimSpace(command.Channel)
	if command.Channel == "" {
		return ReceiveMessageCommand{}, invalidInput("channel", "is required")
	}
	command.ExternalMessageID = strings.TrimSpace(command.ExternalMessageID)
	if command.ExternalMessageID == "" {
		return ReceiveMessageCommand{}, invalidInput("external_message_id", "is required")
	}
	if strings.TrimSpace(command.Content) == "" {
		return ReceiveMessageCommand{}, invalidInput("content", "is required")
	}
	return command, nil
}
