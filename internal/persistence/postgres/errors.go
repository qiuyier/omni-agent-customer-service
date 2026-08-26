package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

var constraintErrors = map[string]error{
	"conversations_pkey": conversation.ErrConversationAlreadyExists,
	"messages_pkey":      conversation.ErrMessageAlreadyExists,
	"messages_channel_external_message_id_unique": conversation.ErrDuplicateExternalMessage,
	"messages_reply_to_message_id_unique":         conversation.ErrReplyAlreadyExists,
	"messages_conversation_channel_fkey":          conversation.ErrConversationNotFound,
	"messages_reply_target_fkey":                  conversation.ErrMessageNotFound,
	"conversations_id_not_blank":                  conversation.ErrInvalidArgument,
	"conversations_customer_id_not_blank":         conversation.ErrInvalidArgument,
	"conversations_channel_not_blank":             conversation.ErrInvalidArgument,
	"conversations_status_valid":                  conversation.ErrInvalidArgument,
	"conversations_agent_id_not_blank":            conversation.ErrInvalidArgument,
	"conversations_agent_assignment_valid":        conversation.ErrInvalidArgument,
	"conversations_revision_positive":             conversation.ErrInvalidArgument,
	"conversations_timestamps_ordered":            conversation.ErrInvalidArgument,
	"messages_id_not_blank":                       conversation.ErrInvalidArgument,
	"messages_conversation_id_not_blank":          conversation.ErrInvalidArgument,
	"messages_channel_not_blank":                  conversation.ErrInvalidArgument,
	"messages_external_id_not_blank":              conversation.ErrInvalidArgument,
	"messages_reply_to_not_blank":                 conversation.ErrInvalidArgument,
	"messages_sender_valid":                       conversation.ErrInvalidArgument,
	"messages_sender_id_not_blank":                conversation.ErrInvalidArgument,
	"messages_actor_id_required":                  conversation.ErrInvalidArgument,
	"messages_reply_sender_valid":                 conversation.ErrInvalidArgument,
	"messages_content_not_blank":                  conversation.ErrInvalidArgument,
}

func databaseError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
	}

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		if domainError, exists := constraintErrors[postgresError.ConstraintName]; exists {
			return fmt.Errorf("%w: postgres %s: %w", domainError, operation, postgresError)
		}
	}
	return fmt.Errorf("postgres: %s: %w", operation, err)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", conversation.ErrInvalidArgument)
	}
	return ctx.Err()
}

func requiredString(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", conversation.ErrInvalidArgument, field)
	}
	return value, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
