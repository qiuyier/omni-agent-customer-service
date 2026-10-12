package postgres

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"strings"
)

func (r *Repository) DeskAttention(ctx context.Context, id, channel, agent string, ids []string) (conversation.Attention, error) {
	return r.deskAttention(ctx, id, channel, agent, ids, nil, false)
}
func (r *Repository) ReadDeskAttention(ctx context.Context, id, channel, agent string, pageIDs []string) (conversation.Attention, error) {
	if len(pageIDs) > 100 {
		return conversation.Attention{}, conversation.ErrInvalidArgument
	}
	if pageIDs == nil {
		pageIDs = []string{}
	}
	return r.deskAttention(ctx, id, channel, agent, nil, pageIDs, true)
}
func (r *Repository) deskAttention(ctx context.Context, id, channel, agent string, markIDs, inspectIDs []string, preview bool) (conversation.Attention, error) {
	result := conversation.Attention{Seen: map[string]bool{}}
	if len(markIDs) > 100 {
		return result, conversation.ErrInvalidArgument
	}
	err := r.WithinTx(ctx, func(port conversation.Transaction) error {
		c, err := port.FindConversation(id)
		if err != nil {
			return err
		}
		if !conversation.CanViewDesk(c, channel, agent) {
			return conversation.ErrAttentionDenied
		}
		tx := port.(*transaction)
		// Validate the entire bounded batch before inserting any receipt; no message body is needed.
		if len(markIDs) > 0 {
			rows, e := tx.tx.Query(ctx, "SELECT id FROM customer_service.messages WHERE conversation_id=$1 AND sender='customer' AND id=ANY($2::text[])", id, markIDs)
			if e != nil {
				return e
			}
			valid := map[string]bool{}
			for rows.Next() {
				var mid string
				if e = rows.Scan(&mid); e != nil {
					rows.Close()
					return e
				}
				valid[mid] = true
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, mid := range markIDs {
				if !valid[mid] {
					return conversation.ErrInvalidArgument
				}
			}
			_, err = tx.tx.Exec(ctx, "INSERT INTO customer_service.desk_read_receipts(agent_id,message_id) SELECT $1,unnest($2::text[]) ON CONFLICT DO NOTHING", agent, markIDs)
			if err != nil {
				return err
			}
		}
		// Count and earliest unread use only message keys and sequence numbers, never content.
		err = tx.tx.QueryRow(ctx, `WITH unread AS (
   SELECT count(*) AS n,min(m.sequence_no) AS first_sequence
   FROM customer_service.messages m
   WHERE m.conversation_id=$1 AND m.sender='customer'
    AND NOT EXISTS(SELECT 1 FROM customer_service.desk_read_receipts r WHERE r.agent_id=$2 AND r.message_id=m.id)
  ) SELECT unread.n,COALESCE(m.id,'') FROM unread LEFT JOIN customer_service.messages m ON m.sequence_no=unread.first_sequence`, id, agent).Scan(&result.Unread, &result.FirstUnreadID)
		if err != nil {
			return err
		}
		columns := messageColumns
		if preview {
			columns = strings.Replace(columns, "    content,", "    left(content,161) AS content,", 1)
		}
		result.Latest, err = queryMessage(ctx, tx.tx, "SELECT "+columns+" FROM customer_service.messages WHERE conversation_id=$1 AND "+publicMessagePredicate+" ORDER BY sequence_no DESC LIMIT 1", conversation.ErrMessageNotFound, "latest desk message", id)
		if err == nil {
			result.HasLatest = true
		} else if !errors.Is(err, conversation.ErrMessageNotFound) {
			return err
		}
		if inspectIDs != nil && len(inspectIDs) == 0 {
			return nil
		}
		predicate := ""
		args := []any{agent, id}
		if inspectIDs != nil {
			predicate = " AND r.message_id=ANY($3::text[])"
			args = append(args, inspectIDs)
		}
		rows, err := tx.tx.Query(ctx, "SELECT r.message_id FROM customer_service.desk_read_receipts r JOIN customer_service.messages m ON m.id=r.message_id WHERE r.agent_id=$1 AND m.conversation_id=$2"+predicate, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var mid string
			if err = rows.Scan(&mid); err != nil {
				return err
			}
			result.Seen[mid] = true
		}
		return rows.Err()
	})
	return result, err
}
