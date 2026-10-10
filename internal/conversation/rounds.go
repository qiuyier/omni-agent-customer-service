package conversation

import (
	"context"
	"time"
)

// Rounds locates an owner's latest round, optionally strictly before a time.
// Browser adapters use immutable creation times, never last-message activity.
type Rounds interface {
	FindLatestRound(context.Context, string, string, time.Time) (*Conversation, error)
}

func (r *MemoryRepository) FindLatestRound(ctx context.Context, customer, channel string, before time.Time) (*Conversation, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest *Conversation
	for _, c := range r.conversations {
		if c.CustomerID() != customer || c.Channel() != channel || (!before.IsZero() && !c.CreatedAt().Before(before)) {
			continue
		}
		if latest == nil || c.CreatedAt().After(latest.CreatedAt()) || (c.CreatedAt().Equal(latest.CreatedAt()) && c.ID() > latest.ID()) {
			latest = c
		}
	}
	if latest == nil {
		return nil, ErrConversationNotFound
	}
	copy := *latest
	return &copy, nil
}
