package customerservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

// ReplyRole is a provider-independent role understood by a ReplyGenerator.
type ReplyRole string

const (
	ReplyRoleSystem    ReplyRole = "system"
	ReplyRoleUser      ReplyRole = "user"
	ReplyRoleAssistant ReplyRole = "assistant"
)

// ReplyTurn is one ordered turn supplied to a reply model.
type ReplyTurn struct {
	Role    ReplyRole
	Content string
}

// ReplyRequest contains the stable business context needed to generate one
// response. TriggerMessageID is the newest customer turn in History.
type ReplyRequest struct {
	ConversationID   string
	TriggerMessageID string
	History          []ReplyTurn
}

// Reply is provider-independent generated content.
type Reply struct {
	Content string
}

// ReplyGenerator is the AI port required by the customer-service application.
// Eino is implemented behind this boundary instead of leaking into the domain.
type ReplyGenerator interface {
	Generate(ctx context.Context, request ReplyRequest) (Reply, error)
}

// ReplyGeneratorFunc adapts a function to ReplyGenerator.
type ReplyGeneratorFunc func(context.Context, ReplyRequest) (Reply, error)

// Generate calls the underlying function.
func (f ReplyGeneratorFunc) Generate(ctx context.Context, request ReplyRequest) (Reply, error) {
	return f(ctx, request)
}

// Option customizes optional Service capabilities.
type Option func(*Service) error

// WithReplyGenerator enables AI reply generation while keeping ReceiveMessage
// usable without an AI provider.
func WithReplyGenerator(generator ReplyGenerator) Option {
	return func(service *Service) error {
		if generator == nil {
			return fmt.Errorf("%w: reply generator is nil", ErrInvalidDependency)
		}
		service.replies = generator
		return nil
	}
}

// WithReplyGenerationTimeout bounds the complete model-generation attempt,
// including retries performed inside a provider SDK. A caller's earlier
// context deadline still takes precedence.
func WithReplyGenerationTimeout(timeout time.Duration) Option {
	return func(service *Service) error {
		if timeout <= 0 {
			return fmt.Errorf(
				"%w: reply generation timeout must be positive",
				ErrInvalidDependency,
			)
		}
		service.replyGenerationTimeout = timeout
		return nil
	}
}

// GenerateBotReplyCommand identifies the customer message to answer.
type GenerateBotReplyCommand struct {
	ConversationID   string
	TriggerMessageID string
}

// GenerateBotReplyResult contains the persisted reply that a channel adapter
// can deliver. Duplicate calls return the original message and content.
type GenerateBotReplyResult struct {
	ConversationID   string
	TriggerMessageID string
	MessageID        string
	Content          string
	Duplicate        bool
}

type replySnapshot struct {
	revision     uint64
	messageCount int
	request      ReplyRequest
}

// GenerateBotReply generates and idempotently persists one bot reply. The
// model call deliberately runs outside repository transactions.
func (s *Service) GenerateBotReply(
	ctx context.Context,
	command GenerateBotReplyCommand,
) (GenerateBotReplyResult, error) {
	if ctx == nil {
		return GenerateBotReplyResult{}, invalidInput("context", "is nil")
	}
	if err := ctx.Err(); err != nil {
		return GenerateBotReplyResult{}, err
	}

	normalized, err := normalizeGenerateBotReplyCommand(command)
	if err != nil {
		return GenerateBotReplyResult{}, err
	}

	var snapshot replySnapshot
	var result GenerateBotReplyResult
	foundExisting := false
	err = s.repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		current, findErr := tx.FindConversation(normalized.ConversationID)
		if findErr != nil {
			return findErr
		}

		existing, findErr := tx.FindReplyByTriggerMessageID(normalized.TriggerMessageID)
		if findErr == nil {
			duplicate, duplicateErr := existingReplyResult(normalized, existing)
			if duplicateErr != nil {
				return duplicateErr
			}
			result = duplicate
			foundExisting = true
			return nil
		}
		if !errors.Is(findErr, conversation.ErrReplyNotFound) {
			return findErr
		}

		if permissionErr := current.CanAcceptMessage(conversation.SenderBot); permissionErr != nil {
			return permissionErr
		}
		messages, listErr := tx.ListMessages(normalized.ConversationID)
		if listErr != nil {
			return listErr
		}
		history, historyErr := buildReplyHistory(messages, normalized.TriggerMessageID)
		if historyErr != nil {
			return historyErr
		}
		snapshot = replySnapshot{
			revision:     current.Revision(),
			messageCount: len(messages),
			request: ReplyRequest{
				ConversationID:   normalized.ConversationID,
				TriggerMessageID: normalized.TriggerMessageID,
				History:          history,
			},
		}
		return nil
	})
	if err != nil {
		return GenerateBotReplyResult{}, err
	}
	if foundExisting {
		return result, nil
	}
	if s.replies == nil {
		return GenerateBotReplyResult{}, fmt.Errorf("%w: reply generator is not configured", ErrReplyUnavailable)
	}

	generationContext := ctx
	cancelGeneration := func() {}
	if s.replyGenerationTimeout > 0 {
		generationContext, cancelGeneration = context.WithTimeout(
			ctx,
			s.replyGenerationTimeout,
		)
	}
	generated, err := s.replies.Generate(generationContext, snapshot.request)
	generationContextErr := generationContext.Err()
	cancelGeneration()
	if generationContextErr != nil {
		err = generationContextErr
	}
	if err != nil {
		return GenerateBotReplyResult{}, fmt.Errorf(
			"%w: generate bot reply: %w",
			ErrReplyGenerationFailed,
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return GenerateBotReplyResult{}, err
	}
	if strings.TrimSpace(generated.Content) == "" {
		return GenerateBotReplyResult{}, ErrEmptyReply
	}

	messageID, err := s.ids.NewMessageID()
	if err != nil {
		return GenerateBotReplyResult{}, fmt.Errorf("generate message ID: %w", err)
	}
	generatedAt := s.clock.Now()

	err = s.repository.WithinTx(ctx, func(tx conversation.Transaction) error {
		current, findErr := tx.FindConversation(normalized.ConversationID)
		if findErr != nil {
			return findErr
		}

		existing, findErr := tx.FindReplyByTriggerMessageID(normalized.TriggerMessageID)
		if findErr == nil {
			duplicate, duplicateErr := existingReplyResult(normalized, existing)
			if duplicateErr != nil {
				return duplicateErr
			}
			result = duplicate
			return nil
		}
		if !errors.Is(findErr, conversation.ErrReplyNotFound) {
			return findErr
		}

		if current.Revision() != snapshot.revision {
			return fmt.Errorf(
				"%w: conversation revision changed from %d to %d",
				ErrReplySuperseded,
				snapshot.revision,
				current.Revision(),
			)
		}
		if permissionErr := current.CanAcceptMessage(conversation.SenderBot); permissionErr != nil {
			return permissionErr
		}
		messages, listErr := tx.ListMessages(normalized.ConversationID)
		if listErr != nil {
			return listErr
		}
		if len(messages) != snapshot.messageCount {
			return fmt.Errorf(
				"%w: conversation message count changed from %d to %d",
				ErrReplySuperseded,
				snapshot.messageCount,
				len(messages),
			)
		}
		if _, historyErr := buildReplyHistory(messages, normalized.TriggerMessageID); historyErr != nil {
			return historyErr
		}

		message, messageErr := conversation.NewMessage(conversation.MessageParams{
			ID:               messageID,
			ConversationID:   normalized.ConversationID,
			ReplyToMessageID: normalized.TriggerMessageID,
			Sender:           conversation.SenderBot,
			Content:          generated.Content,
			CreatedAt:        generatedAt,
		})
		if messageErr != nil {
			return messageErr
		}
		if insertErr := tx.InsertMessage(current.Channel(), message); insertErr != nil {
			return insertErr
		}
		result = GenerateBotReplyResult{
			ConversationID:   normalized.ConversationID,
			TriggerMessageID: normalized.TriggerMessageID,
			MessageID:        message.ID(),
			Content:          message.Content(),
		}
		return nil
	})
	if errors.Is(err, conversation.ErrReplyAlreadyExists) {
		winner, findErr := s.repository.FindReplyByTriggerMessageID(ctx, normalized.TriggerMessageID)
		if findErr != nil {
			return GenerateBotReplyResult{}, fmt.Errorf("find winning reply: %w", findErr)
		}
		return existingReplyResult(normalized, winner)
	}
	if err != nil {
		return GenerateBotReplyResult{}, err
	}
	return result, nil
}

func buildReplyHistory(messages []conversation.Message, triggerMessageID string) ([]ReplyTurn, error) {
	triggerIndex := -1
	latestCustomerIndex := -1
	for index, message := range messages {
		if message.Sender() == conversation.SenderCustomer {
			latestCustomerIndex = index
		}
		if message.ID() == triggerMessageID {
			triggerIndex = index
		}
	}
	if triggerIndex < 0 {
		return nil, fmt.Errorf("%w: message %q was not found in the conversation", ErrInvalidReplyTrigger, triggerMessageID)
	}
	if messages[triggerIndex].Sender() != conversation.SenderCustomer {
		return nil, fmt.Errorf("%w: message %q is not a customer message", ErrInvalidReplyTrigger, triggerMessageID)
	}
	if triggerIndex != latestCustomerIndex {
		return nil, fmt.Errorf("%w: message %q is no longer the latest customer message", ErrReplySuperseded, triggerMessageID)
	}

	history := make([]ReplyTurn, 0, len(messages))
	for _, message := range messages {
		turn, include := replyTurnFromMessage(message)
		if include {
			history = append(history, turn)
		}
	}
	return history, nil
}

func replyTurnFromMessage(message conversation.Message) (ReplyTurn, bool) {
	switch message.Sender() {
	case conversation.SenderCustomer:
		return ReplyTurn{Role: ReplyRoleUser, Content: message.Content()}, true
	case conversation.SenderBot, conversation.SenderHumanAgent:
		return ReplyTurn{Role: ReplyRoleAssistant, Content: message.Content()}, true
	case conversation.SenderSystem:
		return ReplyTurn{Role: ReplyRoleSystem, Content: message.Content()}, true
	case conversation.SenderTool:
		// A raw tool message has no ToolCallID yet, so represent it as system
		// context instead of manufacturing an invalid provider tool message.
		return ReplyTurn{Role: ReplyRoleSystem, Content: "Tool result: " + message.Content()}, true
	default:
		return ReplyTurn{}, false
	}
}

func existingReplyResult(
	command GenerateBotReplyCommand,
	message conversation.Message,
) (GenerateBotReplyResult, error) {
	if message.ConversationID() != command.ConversationID ||
		message.Sender() != conversation.SenderBot ||
		message.ReplyToMessageID() != command.TriggerMessageID {
		return GenerateBotReplyResult{}, fmt.Errorf(
			"%w: trigger message %q does not belong to conversation %q",
			ErrInvalidReplyTrigger,
			command.TriggerMessageID,
			command.ConversationID,
		)
	}
	return GenerateBotReplyResult{
		ConversationID:   command.ConversationID,
		TriggerMessageID: command.TriggerMessageID,
		MessageID:        message.ID(),
		Content:          message.Content(),
		Duplicate:        true,
	}, nil
}

func normalizeGenerateBotReplyCommand(command GenerateBotReplyCommand) (GenerateBotReplyCommand, error) {
	command.ConversationID = strings.TrimSpace(command.ConversationID)
	if command.ConversationID == "" {
		return GenerateBotReplyCommand{}, invalidInput("conversation_id", "is required")
	}
	command.TriggerMessageID = strings.TrimSpace(command.TriggerMessageID)
	if command.TriggerMessageID == "" {
		return GenerateBotReplyCommand{}, invalidInput("trigger_message_id", "is required")
	}
	return command, nil
}
