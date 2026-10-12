package conversation

import (
	"context"
	"errors"
)

var ErrAttentionDenied = errors.New("conversation: attention denied")

type Attention struct {
	FirstUnreadID string
	Unread        int
	Latest        Message
	HasLatest     bool
	Seen          map[string]bool
}

// Exact message receipts: reading an older snapshot never clears newer arrivals.
type DeskAttention interface {
	DeskAttention(context.Context, string, string, string, []string) (Attention, error)
}

func CanViewDesk(c *Conversation, channel, agent string) bool {
	return channel != "" && agent != "" && c.Channel() == channel && (c.Status() == StatusWaitingHuman || c.AgentID() == agent)
}
func SummarizeAttention(all []Message, seen map[string]bool) Attention {
	out := Attention{Seen: seen}
	for _, m := range all {
		if m.Sender() == SenderCustomer && !seen[m.ID()] {
			out.Unread++
			if out.FirstUnreadID == "" {
				out.FirstUnreadID = m.ID()
			}
		}
		if m.Sender() == SenderCustomer || m.Sender() == SenderBot || m.Sender() == SenderHumanAgent {
			out.Latest = m
			out.HasLatest = true
		}
	}
	return out
}
func ValidateSeenMessages(all []Message, ids []string) error {
	if len(ids) > 100 {
		return ErrInvalidArgument
	}
	valid := map[string]bool{}
	for _, m := range all {
		if m.Sender() == SenderCustomer {
			valid[m.ID()] = true
		}
	}
	for _, id := range ids {
		if !valid[id] {
			return ErrInvalidArgument
		}
	}
	return nil
}
func (r *MemoryRepository) DeskAttention(ctx context.Context, id, channel, agent string, ids []string) (Attention, error) {
	if err := contextError(ctx); err != nil {
		return Attention{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := findConversation(r.conversations, id)
	if err != nil {
		return Attention{}, err
	}
	if !CanViewDesk(c, channel, agent) {
		return Attention{}, ErrAttentionDenied
	}
	all := []Message{}
	for _, mid := range r.messageIDsByConv[id] {
		all = append(all, r.messages[mid])
	}
	if err = ValidateSeenMessages(all, ids); err != nil {
		return Attention{}, err
	}
	if r.deskSeen == nil {
		r.deskSeen = map[string]map[string]bool{}
	}
	if r.deskSeen[agent] == nil {
		r.deskSeen[agent] = map[string]bool{}
	}
	for _, mid := range ids {
		r.deskSeen[agent][mid] = true
	}
	seen := map[string]bool{}
	for _, m := range all {
		if r.deskSeen[agent][m.ID()] {
			seen[m.ID()] = true
		}
	}
	return SummarizeAttention(all, seen), nil
}
