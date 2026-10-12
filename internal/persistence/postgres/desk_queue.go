package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

var _ conversation.DeskQueueReader = (*Repository)(nil)

// Filtering and summaries share one statement snapshot. This removes client
// N+1 round trips, not the database work required for exact unread counts.
const deskQueueSQL = `WITH scoped AS MATERIALIZED (
 SELECT id,customer_id,channel,status,agent_id,revision,created_at,updated_at
 FROM customer_service.conversations
 WHERE channel=$1 AND (status='waiting_human' OR (status='human_active' AND agent_id=$2))
 ORDER BY updated_at,id LIMIT 100
), unread AS (
 SELECT m.conversation_id,count(*) AS n,min(m.sequence_no) AS first_sequence
 FROM scoped q JOIN customer_service.messages m ON m.conversation_id=q.id
 WHERE m.sender='customer' AND NOT EXISTS (
   SELECT 1 FROM customer_service.desk_read_receipts r WHERE r.agent_id=$2 AND r.message_id=m.id
 ) GROUP BY m.conversation_id
)
SELECT q.id,q.customer_id,q.channel,q.status,q.agent_id,q.revision,q.created_at,q.updated_at,
 coalesce(u.n,0),coalesce(first.id,''),latest.id,latest.sender,latest.sender_id,latest.content,latest.created_at
FROM scoped q
LEFT JOIN unread u ON u.conversation_id=q.id
LEFT JOIN customer_service.messages first ON first.sequence_no=u.first_sequence
LEFT JOIN LATERAL (
 SELECT id,sender,sender_id,left(content,161) AS content,created_at
 FROM customer_service.messages WHERE conversation_id=q.id AND sender IN ('customer','bot','human_agent')
 ORDER BY sequence_no DESC LIMIT 1
) latest ON true
ORDER BY q.updated_at,q.id`

func (r *Repository) ReadDeskQueue(ctx context.Context, channel, agent string) ([]conversation.DeskQueueItem, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	var err error
	if channel, err = requiredString("channel", channel); err != nil {
		return nil, err
	}
	if agent, err = requiredString("agent", agent); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, deskQueueSQL, channel, agent)
	if err != nil {
		return nil, databaseError(ctx, "read desk queue", err)
	}
	defer rows.Close()
	items := make([]conversation.DeskQueueItem, 0)
	for rows.Next() {
		var p conversation.RestoreParams
		var revision int64
		var status string
		var agentID, latestID, sender, senderID, content pgtype.Text
		var latestAt pgtype.Timestamptz
		var info conversation.Attention
		if err := rows.Scan(&p.ID, &p.CustomerID, &p.Channel, &status, &agentID, &revision, &p.CreatedAt, &p.UpdatedAt,
			&info.Unread, &info.FirstUnreadID, &latestID, &sender, &senderID, &content, &latestAt); err != nil {
			return nil, databaseError(ctx, "scan desk queue", err)
		}
		if revision <= 0 {
			return nil, fmt.Errorf("postgres: invalid queue revision %d", revision)
		}
		p.Status = conversation.Status(status)
		p.AgentID = nullableTextValue(agentID)
		p.Revision = uint64(revision)
		c, err := conversation.Restore(p)
		if err != nil {
			return nil, fmt.Errorf("postgres: invalid queue conversation: %w", err)
		}
		if latestID.Valid {
			info.Latest, err = conversation.NewMessage(conversation.MessageParams{
				ID: latestID.String, ConversationID: p.ID, Sender: conversation.Sender(sender.String),
				SenderID: nullableTextValue(senderID), Content: content.String, CreatedAt: latestAt.Time,
			})
			if err != nil {
				return nil, fmt.Errorf("postgres: invalid queue preview: %w", err)
			}
			info.HasLatest = true
		}
		items = append(items, conversation.DeskQueueItem{Conversation: c, Attention: info})
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError(ctx, "iterate desk queue", err)
	}
	return items, nil
}
