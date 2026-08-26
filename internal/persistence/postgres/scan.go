package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type queryExecutor interface {
	queryRower
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func queryConversation(
	ctx context.Context,
	queryer queryRower,
	query string,
	id string,
) (*conversation.Conversation, error) {
	current, err := scanConversation(queryer.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", conversation.ErrConversationNotFound, id)
	}
	if err != nil {
		return nil, databaseError(ctx, "find conversation", err)
	}
	return current, nil
}

func scanConversation(row pgx.Row) (*conversation.Conversation, error) {
	var (
		id         string
		customerID string
		channel    string
		status     string
		agentID    pgtype.Text
		revision   int64
		createdAt  time.Time
		updatedAt  time.Time
	)
	if err := row.Scan(
		&id,
		&customerID,
		&channel,
		&status,
		&agentID,
		&revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	if revision <= 0 {
		return nil, fmt.Errorf("postgres: persisted conversation has invalid revision %d", revision)
	}

	current, err := conversation.Restore(conversation.RestoreParams{
		ID:         id,
		CustomerID: customerID,
		Channel:    channel,
		Status:     conversation.Status(status),
		AgentID:    nullableTextValue(agentID),
		Revision:   uint64(revision),
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: persisted conversation is invalid: %v", err)
	}
	return current, nil
}

func queryMessage(
	ctx context.Context,
	queryer queryRower,
	query string,
	notFound error,
	operation string,
	arguments ...any,
) (conversation.Message, error) {
	message, err := scanMessage(queryer.QueryRow(ctx, query, arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return conversation.Message{}, notFound
	}
	if err != nil {
		return conversation.Message{}, databaseError(ctx, operation, err)
	}
	return message, nil
}

func scanMessage(row pgx.Row) (conversation.Message, error) {
	var (
		id                string
		conversationID    string
		externalMessageID pgtype.Text
		replyToMessageID  pgtype.Text
		sender            string
		senderID          pgtype.Text
		content           string
		createdAt         time.Time
	)
	if err := row.Scan(
		&id,
		&conversationID,
		&externalMessageID,
		&replyToMessageID,
		&sender,
		&senderID,
		&content,
		&createdAt,
	); err != nil {
		return conversation.Message{}, err
	}

	message, err := conversation.NewMessage(conversation.MessageParams{
		ID:                id,
		ConversationID:    conversationID,
		ExternalMessageID: nullableTextValue(externalMessageID),
		ReplyToMessageID:  nullableTextValue(replyToMessageID),
		Sender:            conversation.Sender(sender),
		SenderID:          nullableTextValue(senderID),
		Content:           content,
		CreatedAt:         createdAt,
	})
	if err != nil {
		return conversation.Message{}, fmt.Errorf("postgres: persisted message is invalid: %v", err)
	}
	return message, nil
}

func queryMessages(
	ctx context.Context,
	queryer queryExecutor,
	conversationID string,
) ([]conversation.Message, error) {
	rows, err := queryer.Query(ctx, listMessagesSQL, conversationID)
	if err != nil {
		return nil, databaseError(ctx, "list messages", err)
	}
	defer rows.Close()

	messages := make([]conversation.Message, 0)
	for rows.Next() {
		message, scanErr := scanMessage(rows)
		if scanErr != nil {
			return nil, databaseError(ctx, "scan message", scanErr)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError(ctx, "iterate messages", err)
	}
	return messages, nil
}

func nullableTextValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
