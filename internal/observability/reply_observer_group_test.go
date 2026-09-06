package observability_test

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/observability"
)

func TestNewReplyObserverGroupRejectsInvalidDependencies(t *testing.T) {
	for name, observers := range map[string][]customerservice.ReplyObserver{
		"empty":     nil,
		"nil child": {nil},
		"later nil": {&groupTestObserver{}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			group, err := observability.NewReplyObserverGroup(observers...)
			if err == nil || group != nil {
				t.Fatalf("NewReplyObserverGroup() = (%v, %v), want nil group and dependency error", group, err)
			}
		})
	}
}

func TestReplyObserverGroupForwardsEventsInOrderAndIsolatesPanics(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestIDContextKey, "request-123")
	operation := customerservice.ReplyOperation{
		Mode:             customerservice.ReplyModeStream,
		ConversationID:   "conversation-1",
		TriggerMessageID: "message-1",
	}
	started := customerservice.ReplyStartedEvent{
		Operation: operation,
		Capacity:  customerservice.ReplyCapacitySnapshot{Active: 1, Limit: 8},
	}
	rejected := customerservice.ReplyRejectedEvent{
		Operation: operation,
		Capacity:  customerservice.ReplyCapacitySnapshot{Active: 8, Limit: 8},
	}
	finished := customerservice.ReplyFinishedEvent{
		Operation:     operation,
		Outcome:       customerservice.ReplyOutcomeCompleted,
		Phase:         customerservice.ReplyPhasePersist,
		Capacity:      customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 8},
		ModelDuration: 250 * time.Millisecond,
		TotalDuration: 300 * time.Millisecond,
		Duplicate:     true,
		CleanupFailed: true,
	}
	var calls []string
	child := func(name string, shouldPanic bool) *groupTestObserver {
		check := func(gotCtx context.Context, gotEvent, wantEvent any, kind string) {
			t.Helper()
			if gotCtx != ctx || !reflect.DeepEqual(gotEvent, wantEvent) {
				t.Errorf("%s %s did not receive the original context and event", name, kind)
			}
			calls = append(calls, name+"."+kind)
			if shouldPanic {
				panic("broken observer")
			}
		}
		return &groupTestObserver{
			started: func(ctx context.Context, event customerservice.ReplyStartedEvent) {
				check(ctx, event, started, "started")
			},
			rejected: func(ctx context.Context, event customerservice.ReplyRejectedEvent) {
				check(ctx, event, rejected, "rejected")
			},
			finished: func(ctx context.Context, event customerservice.ReplyFinishedEvent) {
				check(ctx, event, finished, "finished")
			},
		}
	}
	group, err := observability.NewReplyObserverGroup(child("first", false), child("broken", true), child("last", false))
	if err != nil {
		t.Fatal(err)
	}
	group.ReplyStarted(ctx, started)
	group.ReplyRejected(ctx, rejected)
	group.ReplyFinished(ctx, finished)
	want := []string{
		"first.started", "broken.started", "last.started",
		"first.rejected", "broken.rejected", "last.rejected",
		"first.finished", "broken.finished", "last.finished",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("forwarding order = %v, want %v", calls, want)
	}
}

func TestReplyObserverGroupOwnsItsObserverSliceAndSupportsConcurrentEvents(t *testing.T) {
	var calls atomic.Int64
	observer := &groupTestObserver{
		started:  func(context.Context, customerservice.ReplyStartedEvent) { calls.Add(1) },
		rejected: func(context.Context, customerservice.ReplyRejectedEvent) { calls.Add(1) },
		finished: func(context.Context, customerservice.ReplyFinishedEvent) { calls.Add(1) },
	}
	observers := []customerservice.ReplyObserver{observer}
	group, err := observability.NewReplyObserverGroup(observers...)
	if err != nil {
		t.Fatal(err)
	}
	observers[0] = nil // The caller no longer owns the group's observer list.
	const workers = 12
	const attempts = 50
	var workersDone sync.WaitGroup
	for range workers {
		workersDone.Add(1)
		go func() {
			defer workersDone.Done()
			for range attempts {
				group.ReplyStarted(context.Background(), customerservice.ReplyStartedEvent{})
				group.ReplyRejected(context.Background(), customerservice.ReplyRejectedEvent{})
				group.ReplyFinished(context.Background(), customerservice.ReplyFinishedEvent{})
			}
		}()
	}
	workersDone.Wait()
	if got, want := calls.Load(), int64(workers*attempts*3); got != want {
		t.Fatalf("forwarded calls = %d, want %d", got, want)
	}
}

type groupTestObserver struct {
	started  func(context.Context, customerservice.ReplyStartedEvent)
	rejected func(context.Context, customerservice.ReplyRejectedEvent)
	finished func(context.Context, customerservice.ReplyFinishedEvent)
}

func (o *groupTestObserver) ReplyStarted(ctx context.Context, event customerservice.ReplyStartedEvent) {
	if o.started != nil {
		o.started(ctx, event)
	}
}

func (o *groupTestObserver) ReplyRejected(ctx context.Context, event customerservice.ReplyRejectedEvent) {
	if o.rejected != nil {
		o.rejected(ctx, event)
	}
}

func (o *groupTestObserver) ReplyFinished(ctx context.Context, event customerservice.ReplyFinishedEvent) {
	if o.finished != nil {
		o.finished(ctx, event)
	}
}
