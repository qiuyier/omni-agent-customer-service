package postgres

import (
	"context"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

// Literal, case-sensitive substring matching; % and _ are not wildcards.
// EXISTS excludes private messages, and LIMIT bounds returned rows.
func (r *Repository) SearchDesk(ctx context.Context, channel, agent string, q conversation.DeskSearchQuery) (conversation.DeskSearchPage, error) {
	out := conversation.DeskSearchPage{Items: []conversation.DeskSearchItem{}}
	if !q.Valid() || channel == "" || agent == "" {
		return out, conversation.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `SELECT c.id,c.status,c.revision FROM customer_service.conversations c
 WHERE c.channel=$1 AND (c.status='waiting_human' OR c.agent_id=$2)
 AND ($3='' OR c.status=$3) AND c.id COLLATE "C">$4 COLLATE "C"
 AND strpos(c.id,$5)>0
 AND ($6='' OR EXISTS(SELECT 1 FROM customer_service.messages m WHERE m.conversation_id=c.id
 AND m.sender IN ('customer','bot','human_agent') AND strpos(m.content,$6)>0))
 ORDER BY c.id COLLATE "C" LIMIT 21`, channel, agent, q.Status, q.After, q.ID, q.Keyword)
	if err != nil {
		return out, databaseError(ctx, "search desk", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item conversation.DeskSearchItem
		if err := rows.Scan(&item.ID, &item.Status, &item.Revision); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, databaseError(ctx, "search desk", err)
	}
	if len(out.Items) > conversation.DeskSearchLimit {
		out.Items = out.Items[:conversation.DeskSearchLimit]
		out.Next = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
