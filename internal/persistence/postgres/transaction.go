package postgres

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

const rollbackTimeout = 5 * time.Second

// WithinTx runs fn in a short READ COMMITTED transaction. The callback must
// not escape or use its Transaction concurrently, and must not call an AI
// model or another remote service while database locks are held.
func (r *Repository) WithinTx(
	ctx context.Context,
	fn func(conversation.Transaction) error,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := r.validate(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("%w: transaction callback is nil", conversation.ErrInvalidArgument)
	}

	databaseTx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return databaseError(ctx, "begin transaction", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = databaseTx.Rollback(rollbackContext)
	}()

	tx := &transaction{ctx: ctx, tx: databaseTx}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := databaseTx.Commit(ctx); err != nil {
		return databaseError(ctx, "commit transaction", err)
	}
	return nil
}

type transaction struct {
	ctx context.Context
	tx  pgx.Tx
}

func (tx *transaction) FindConversation(id string) (*conversation.Conversation, error) {
	id, err := requiredString("id", id)
	if err != nil {
		return nil, err
	}
	if err := tx.lock("conversation", id); err != nil {
		return nil, err
	}
	return queryConversation(tx.ctx, tx.tx, findConversationForUpdateSQL, id)
}

func (tx *transaction) InsertConversation(current *conversation.Conversation) error {
	validated, err := validateConversation(current)
	if err != nil {
		return err
	}
	if err := tx.lock("conversation", validated.ID()); err != nil {
		return err
	}

	result, err := tx.tx.Exec(
		tx.ctx,
		insertConversationSQL,
		validated.ID(),
		validated.CustomerID(),
		validated.Channel(),
		validated.Status(),
		nullableString(validated.AgentID()),
		validated.Revision(),
		validated.CreatedAt(),
		validated.UpdatedAt(),
	)
	if err != nil {
		return databaseError(tx.ctx, "insert conversation", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", conversation.ErrConversationAlreadyExists, validated.ID())
	}
	return nil
}

func (tx *transaction) UpdateConversation(current *conversation.Conversation) error {
	validated, err := validateConversation(current)
	if err != nil {
		return err
	}
	if _, err := tx.FindConversation(validated.ID()); err != nil {
		return err
	}

	result, err := tx.tx.Exec(
		tx.ctx,
		updateConversationSQL,
		validated.ID(),
		validated.Status(),
		nullableString(validated.AgentID()),
		validated.Revision(),
		validated.UpdatedAt(),
	)
	if err != nil {
		return databaseError(tx.ctx, "update conversation", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", conversation.ErrConversationNotFound, validated.ID())
	}
	return nil
}

func (tx *transaction) FindMessageByExternalID(
	channel string,
	externalMessageID string,
) (conversation.Message, error) {
	channel, err := requiredString("channel", channel)
	if err != nil {
		return conversation.Message{}, err
	}
	externalMessageID, err = requiredString("external_message_id", externalMessageID)
	if err != nil {
		return conversation.Message{}, err
	}
	return queryMessage(
		tx.ctx,
		tx.tx,
		findMessageByExternalIDSQL,
		conversation.ErrMessageNotFound,
		"find message by external ID",
		channel,
		externalMessageID,
	)
}

func (tx *transaction) FindReplyByTriggerMessageID(
	triggerMessageID string,
) (conversation.Message, error) {
	triggerMessageID, err := requiredString("trigger_message_id", triggerMessageID)
	if err != nil {
		return conversation.Message{}, err
	}
	return queryMessage(
		tx.ctx,
		tx.tx,
		findReplyByTriggerMessageIDSQL,
		conversation.ErrReplyNotFound,
		"find reply by trigger message ID",
		triggerMessageID,
	)
}

func (tx *transaction) ListMessages(conversationID string) ([]conversation.Message, error) {
	conversationID, err := requiredString("conversation_id", conversationID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.FindConversation(conversationID); err != nil {
		return nil, err
	}
	return queryMessages(tx.ctx, tx.tx, conversationID)
}

func (tx *transaction) InsertMessage(channel string, message conversation.Message) error {
	channel, err := requiredString("channel", channel)
	if err != nil {
		return err
	}
	validated, err := validateMessage(message)
	if err != nil {
		return err
	}

	current, err := tx.FindConversation(validated.ConversationID())
	if err != nil {
		return err
	}
	if current.Channel() != channel {
		return fmt.Errorf(
			"%w: channel does not match the conversation channel",
			conversation.ErrInvalidArgument,
		)
	}
	if triggerMessageID := validated.ReplyToMessageID(); triggerMessageID != "" {
		trigger, findErr := queryMessage(
			tx.ctx,
			tx.tx,
			findMessageByIDSQL,
			conversation.ErrMessageNotFound,
			"find reply target",
			triggerMessageID,
		)
		if findErr != nil {
			return fmt.Errorf("reply target %q: %w", triggerMessageID, findErr)
		}
		if trigger.ConversationID() != validated.ConversationID() {
			return fmt.Errorf(
				"%w: reply_to_message_id belongs to another conversation",
				conversation.ErrInvalidArgument,
			)
		}
		if trigger.Sender() != conversation.SenderCustomer {
			return fmt.Errorf(
				"%w: reply_to_message_id must reference a customer message",
				conversation.ErrInvalidArgument,
			)
		}
	}

	result, err := tx.tx.Exec(
		tx.ctx,
		insertMessageSQL,
		validated.ID(),
		validated.ConversationID(),
		channel,
		nullableString(validated.ExternalMessageID()),
		nullableString(validated.ReplyToMessageID()),
		validated.Sender(),
		nullableString(validated.SenderID()),
		validated.Content(),
		validated.CreatedAt(),
	)
	if err != nil {
		return databaseError(tx.ctx, "insert message", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	return tx.classifyMessageConflict(channel, validated)
}

func (tx *transaction) classifyMessageConflict(
	channel string,
	message conversation.Message,
) error {
	if _, err := queryMessage(
		tx.ctx,
		tx.tx,
		findMessageByIDSQL,
		conversation.ErrMessageNotFound,
		"classify message ID conflict",
		message.ID(),
	); err == nil {
		return fmt.Errorf("%w: %q", conversation.ErrMessageAlreadyExists, message.ID())
	} else if !errors.Is(err, conversation.ErrMessageNotFound) {
		return err
	}

	if externalMessageID := message.ExternalMessageID(); externalMessageID != "" {
		if _, err := queryMessage(
			tx.ctx,
			tx.tx,
			findMessageByExternalIDSQL,
			conversation.ErrMessageNotFound,
			"classify external message conflict",
			channel,
			externalMessageID,
		); err == nil {
			return fmt.Errorf(
				"%w: channel %q external ID %q",
				conversation.ErrDuplicateExternalMessage,
				channel,
				externalMessageID,
			)
		} else if !errors.Is(err, conversation.ErrMessageNotFound) {
			return err
		}
	}

	if triggerMessageID := message.ReplyToMessageID(); triggerMessageID != "" {
		if _, err := tx.FindReplyByTriggerMessageID(triggerMessageID); err == nil {
			return fmt.Errorf(
				"%w: trigger message %q",
				conversation.ErrReplyAlreadyExists,
				triggerMessageID,
			)
		} else if !errors.Is(err, conversation.ErrReplyNotFound) {
			return err
		}
	}

	return errors.New("postgres: message insert conflicted without a matching domain key")
}

func (tx *transaction) lock(namespace string, values ...string) error {
	if _, err := tx.tx.Exec(tx.ctx, advisoryLockSQL, advisoryLockKey(namespace, values...)); err != nil {
		return databaseError(tx.ctx, "acquire transaction lock", err)
	}
	return nil
}

func advisoryLockKey(namespace string, values ...string) int64 {
	hash := fnv.New64a()
	writeHashPart(hash, namespace)
	for _, value := range values {
		writeHashPart(hash, value)
	}
	return int64(hash.Sum64())
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashPart(writer hashWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func validateConversation(current *conversation.Conversation) (*conversation.Conversation, error) {
	if current == nil {
		return nil, fmt.Errorf("%w: conversation is nil", conversation.ErrInvalidArgument)
	}
	return conversation.Restore(conversation.RestoreParams{
		ID:         current.ID(),
		CustomerID: current.CustomerID(),
		Channel:    current.Channel(),
		Status:     current.Status(),
		AgentID:    current.AgentID(),
		Revision:   current.Revision(),
		CreatedAt:  current.CreatedAt(),
		UpdatedAt:  current.UpdatedAt(),
	})
}

func validateMessage(message conversation.Message) (conversation.Message, error) {
	return conversation.NewMessage(conversation.MessageParams{
		ID:                message.ID(),
		ConversationID:    message.ConversationID(),
		ExternalMessageID: message.ExternalMessageID(),
		ReplyToMessageID:  message.ReplyToMessageID(),
		Sender:            message.Sender(),
		SenderID:          message.SenderID(),
		Content:           message.Content(),
		CreatedAt:         message.CreatedAt(),
	})
}
