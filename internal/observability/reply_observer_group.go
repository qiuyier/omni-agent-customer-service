package observability

import (
	"context"
	"fmt"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// ReplyObserverGroup forwards each event to its observers in registration order.
// The list is immutable; each child must satisfy ReplyObserver's concurrency and
// prompt-return requirements.
type ReplyObserverGroup struct {
	observers []customerservice.ReplyObserver
}

func NewReplyObserverGroup(observers ...customerservice.ReplyObserver) (*ReplyObserverGroup, error) {
	if len(observers) == 0 {
		return nil, fmt.Errorf("%w: reply observer group is empty", errInvalidDependency)
	}
	for index, observer := range observers {
		if observer == nil {
			return nil, fmt.Errorf("%w: reply observer at index %d is nil", errInvalidDependency, index)
		}
	}
	return &ReplyObserverGroup{
		observers: append([]customerservice.ReplyObserver(nil), observers...),
	}, nil
}

func (g *ReplyObserverGroup) ReplyStarted(ctx context.Context, event customerservice.ReplyStartedEvent) {
	for _, observer := range g.observers {
		notifyReplyObserver(func() { observer.ReplyStarted(ctx, event) })
	}
}

func (g *ReplyObserverGroup) ReplyRejected(ctx context.Context, event customerservice.ReplyRejectedEvent) {
	for _, observer := range g.observers {
		notifyReplyObserver(func() { observer.ReplyRejected(ctx, event) })
	}
}

func (g *ReplyObserverGroup) ReplyFinished(ctx context.Context, event customerservice.ReplyFinishedEvent) {
	for _, observer := range g.observers {
		notifyReplyObserver(func() { observer.ReplyFinished(ctx, event) })
	}
}

// Recover around each child, not the entire loop: a failed logger must not
// prevent the metrics observer from receiving the same event (or vice versa).
func notifyReplyObserver(notify func()) {
	defer func() { _ = recover() }()
	notify()
}

var _ customerservice.ReplyObserver = (*ReplyObserverGroup)(nil)
