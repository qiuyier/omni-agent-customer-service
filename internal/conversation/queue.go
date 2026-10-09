package conversation

import (
	"context"
	"sort"
)

// HumanQueue is a bounded, channel-scoped projection, never a service-wide list.
type HumanQueue interface {
	ListHumanQueue(context.Context, string, string) ([]*Conversation, error)
}

func (r *MemoryRepository) ListHumanQueue(ctx context.Context, channel, agent string) ([]*Conversation, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := []*Conversation{}
	for _, c := range r.conversations {
		if c.Channel() == channel && (c.Status() == StatusWaitingHuman || (c.Status() == StatusHumanActive && c.AgentID() == agent)) {
			copy := *c
			result = append(result, &copy)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt().Equal(result[j].UpdatedAt()) {
			return result[i].ID() < result[j].ID()
		}
		return result[i].UpdatedAt().Before(result[j].UpdatedAt())
	})
	if len(result) > 100 {
		result = result[:100]
	}
	return result, nil
}
