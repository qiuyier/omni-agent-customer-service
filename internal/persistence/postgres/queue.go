package postgres

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func (r *Repository) ListHumanQueue(ctx context.Context, channel, agent string) ([]*conversation.Conversation, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+conversationColumns+` FROM customer_service.conversations WHERE channel=$1 AND (status='waiting_human' OR (status='human_active' AND agent_id=$2)) ORDER BY updated_at,id LIMIT 100`, channel, agent)
	if err != nil {
		return nil, databaseError(ctx, "list human queue", err)
	}
	defer rows.Close()
	result := []*conversation.Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
