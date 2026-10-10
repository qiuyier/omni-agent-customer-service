package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"time"
)

func (r *Repository) FindLatestRound(ctx context.Context, customer, channel string, before time.Time) (*conversation.Conversation, error) {
	query := `SELECT ` + conversationColumns + ` FROM customer_service.conversations WHERE customer_id=$1 AND channel=$2`
	args := []any{customer, channel}
	if !before.IsZero() {
		query += ` AND created_at<$3`
		args = append(args, before)
	}
	c, err := scanConversation(r.pool.QueryRow(ctx, query+` ORDER BY created_at DESC,id DESC LIMIT 1`, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, conversation.ErrConversationNotFound
	}
	if err != nil {
		return nil, databaseError(ctx, "find latest round", err)
	}
	return c, nil
}
