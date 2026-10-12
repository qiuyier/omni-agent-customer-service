package postgres

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func (r *Repository) DeskAttention(ctx context.Context, id, channel, agent string, ids []string) (conversation.Attention, error) {
	var result conversation.Attention
	err := r.WithinTx(ctx, func(port conversation.Transaction) error {
		c, err := port.FindConversation(id)
		if err != nil {
			return err
		}
		if !conversation.CanViewDesk(c, channel, agent) {
			return conversation.ErrAttentionDenied
		}
		all, err := port.ListMessages(id)
		if err != nil {
			return err
		}
		if err = conversation.ValidateSeenMessages(all, ids); err != nil {
			return err
		}
		tx := port.(*transaction)
		for _, mid := range ids {
			if _, err = tx.tx.Exec(ctx, `INSERT INTO customer_service.desk_read_receipts(agent_id,message_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, agent, mid); err != nil {
				return err
			}
		}
		rows, err := tx.tx.Query(ctx, `SELECT r.message_id FROM customer_service.desk_read_receipts r JOIN customer_service.messages m ON m.id=r.message_id WHERE r.agent_id=$1 AND m.conversation_id=$2`, agent, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var mid string
			if err = rows.Scan(&mid); err != nil {
				return err
			}
			seen[mid] = true
		}
		if err = rows.Err(); err != nil {
			return err
		}
		result = conversation.SummarizeAttention(all, seen)
		return nil
	})
	return result, err
}
