package postgres

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"strings"
	"unicode/utf8"
)

const publicMessagePredicate = "sender IN ('customer','bot','human_agent')"

// Only indexed IDs, ordering keys and byte lengths are returned for at most 100 candidates.
// octet_length(text) can read a toasted datum's size without transferring its body.
func (tx *transaction) PageMessages(id, before, after, from string) (conversation.HistoryPage, error) {
	out := conversation.HistoryPage{Messages: []conversation.Message{}, Truncated: map[string]bool{}}
	if _, err := tx.FindConversation(id); err != nil {
		return out, err
	}
	count := 0
	cursor := ""
	for _, v := range []string{before, after, from} {
		if v != "" {
			count++
			cursor = v
		}
	}
	if count > 1 {
		return out, conversation.ErrHistoryCursor
	}
	forward := after != "" || from != ""
	var sequence int64
	if cursor != "" {
		err := tx.tx.QueryRow(tx.ctx, "SELECT sequence_no FROM customer_service.messages WHERE conversation_id=$1 AND id=$2 AND "+publicMessagePredicate, id, cursor).Scan(&sequence)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, conversation.ErrHistoryCursor
		}
		if err != nil {
			return out, err
		}
	}
	predicate := ""
	args := []any{id}
	if cursor != "" {
		op := "<"
		if after != "" {
			op = ">"
		}
		if from != "" {
			op = ">="
		}
		predicate = " AND sequence_no " + op + " $2"
		args = append(args, sequence)
	}
	order := "DESC"
	if forward {
		order = "ASC"
	}
	rows, err := tx.tx.Query(tx.ctx, "SELECT id,sequence_no,octet_length(content) FROM customer_service.messages WHERE conversation_id=$1 AND "+publicMessagePredicate+predicate+" ORDER BY sequence_no "+order+" LIMIT 100", args...)
	if err != nil {
		return out, err
	}
	type candidate struct {
		id   string
		seq  int64
		size int
	}
	selected := []candidate{}
	budget := 128 << 10
	for rows.Next() {
		var m candidate
		if err = rows.Scan(&m.id, &m.seq, &m.size); err != nil {
			rows.Close()
			return out, err
		}
		if m.size > budget && len(selected) > 0 {
			break
		}
		selected = append(selected, m)
		budget -= m.size
		if budget <= 0 {
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(selected) == 0 {
		if cursor != "" {
			out.HasOlder = forward
			out.HasNewer = !forward
		}
		return out, nil
	}
	if !forward {
		for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
			selected[i], selected[j] = selected[j], selected[i]
		}
	}
	for _, m := range selected {
		if m.size > 128<<10 {
			out.Truncated[m.id] = true
		}
	}
	// The range is bounded by the selected adjacent public rows. LIMIT also bounds returned bodies.
	columns := strings.Replace(messageColumns, "    content,", "    left(content,131072) AS content,", 1)
	rows, err = tx.tx.Query(tx.ctx, "SELECT "+columns+" FROM customer_service.messages WHERE conversation_id=$1 AND sequence_no >= $2 AND sequence_no <= $3 AND "+publicMessagePredicate+" ORDER BY sequence_no LIMIT 100", id, selected[0].seq, selected[len(selected)-1].seq)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p conversation.MessageParams
		var external, reply, senderID *string
		if err = rows.Scan(&p.ID, &p.ConversationID, &external, &reply, &p.Sender, &senderID, &p.Content, &p.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		if external != nil {
			p.ExternalMessageID = *external
		}
		if reply != nil {
			p.ReplyToMessageID = *reply
		}
		if senderID != nil {
			p.SenderID = *senderID
		}
		if len(p.Content) > 128<<10 {
			p.Content = p.Content[:128<<10]
			for !utf8.ValidString(p.Content) {
				p.Content = p.Content[:len(p.Content)-1]
			}
		}
		m, e := conversation.NewMessage(p)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Messages = append(out.Messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	err = tx.tx.QueryRow(tx.ctx, "SELECT EXISTS(SELECT 1 FROM customer_service.messages WHERE conversation_id=$1 AND sequence_no<$2 AND "+publicMessagePredicate+"), EXISTS(SELECT 1 FROM customer_service.messages WHERE conversation_id=$1 AND sequence_no>$3 AND "+publicMessagePredicate+")", id, selected[0].seq, selected[len(selected)-1].seq).Scan(&out.HasOlder, &out.HasNewer)
	return out, err
}
