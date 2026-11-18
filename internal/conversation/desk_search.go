package conversation

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

// DeskSearchLimit bounds metadata returned per page, not the database scan cost.
const DeskSearchLimit = 20

type DeskSearchQuery struct {
	Keyword string `json:"keyword"`
	ID      string `json:"conversation_id"`
	Status  string `json:"status"`
	After   string `json:"after"`
}
type DeskSearchItem struct {
	ID       string `json:"conversation_id"`
	Status   Status `json:"status"`
	Revision uint64 `json:"revision"`
}
type DeskSearchPage struct {
	Items []DeskSearchItem `json:"items"`
	Next  string           `json:"next,omitempty"`
}
type DeskSearcher interface {
	SearchDesk(context.Context, string, string, DeskSearchQuery) (DeskSearchPage, error)
}

func (q DeskSearchQuery) Valid() bool {
	if len(q.Keyword) > 256 || len(q.ID) > 128 || len(q.After) > 128 {
		return false
	}
	for _, s := range []string{q.Keyword, q.ID, q.After} {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return false
		}
	}
	return q.Status == "" || q.Status == "waiting_human" || q.Status == "human_active" || q.Status == "closed"
}

// Search uses the history authorization boundary; results are not capabilities.
// Metadata only: never return customer IDs or matched message bodies.
func (r *MemoryRepository) SearchDesk(ctx context.Context, channel, agent string, q DeskSearchQuery) (DeskSearchPage, error) {
	out := DeskSearchPage{Items: []DeskSearchItem{}}
	if !q.Valid() || channel == "" || agent == "" {
		return out, ErrInvalidArgument
	}
	if err := contextError(ctx); err != nil {
		return out, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.conversations {
		if err := contextError(ctx); err != nil {
			return out, err
		}
		if c.Channel() != channel || (c.Status() != StatusWaitingHuman && c.AgentID() != agent) || (q.Status != "" && string(c.Status()) != q.Status) || c.ID() <= q.After || !strings.Contains(c.ID(), q.ID) {
			continue
		}
		match := q.Keyword == ""
		if !match {
			for _, id := range r.messageIDsByConv[c.ID()] {
				if err := contextError(ctx); err != nil {
					return out, err
				}
				m := r.messages[id]
				if (m.Sender() == SenderCustomer || m.Sender() == SenderBot || m.Sender() == SenderHumanAgent) && strings.Contains(m.Content(), q.Keyword) {
					match = true
					break
				}
			}
		}
		if match {
			out.Items = append(out.Items, DeskSearchItem{ID: c.ID(), Status: c.Status(), Revision: c.Revision()})
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	if len(out.Items) > DeskSearchLimit {
		out.Items = out.Items[:DeskSearchLimit]
		out.Next = out.Items[len(out.Items)-1].ID
	}
	return out, nil
}
