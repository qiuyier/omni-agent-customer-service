package customerservice_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestWithReplyObserverRejectsNilObserver(t *testing.T) {
	service, err := customerservice.New(
		conversation.NewMemoryRepository(),
		&sequenceIDGenerator{},
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyObserver(nil),
	)
	if !errors.Is(err, customerservice.ErrInvalidDependency) {
		t.Fatalf("customerservice.New() error = %v, want ErrInvalidDependency", err)
	}
	if service != nil {
		t.Fatalf("customerservice.New() service = %#v, want nil", service)
	}
}

func TestReplyObserverRecordsGenerateLifecycleWithoutChargingDuplicates(t *testing.T) {
	observer := &recordingReplyObserver{}
	service := newObservedReplyService(
		t,
		customerservice.ReplyGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			return customerservice.Reply{Content: "answer"}, nil
		}),
		nil,
		observer,
		1,
	)
	command := receiveReplyConcurrencyMessage(t, service, "observer-generate")

	result, err := service.GenerateBotReply(context.Background(), command)
	if err != nil {
		t.Fatalf("GenerateBotReply() error = %v", err)
	}
	if result.Duplicate {
		t.Fatal("GenerateBotReply() duplicate = true, want false")
	}

	started, rejected, finished := observer.snapshot()
	if len(started) != 1 || len(rejected) != 0 || len(finished) != 1 {
		t.Fatalf(
			"observer events: started=%d rejected=%d finished=%d, want 1/0/1",
			len(started),
			len(rejected),
			len(finished),
		)
	}
	assertReplyOperation(t, started[0].Operation, customerservice.ReplyModeGenerate, command)
	if started[0].Capacity != (customerservice.ReplyCapacitySnapshot{Active: 1, Limit: 1}) {
		t.Fatalf("started capacity = %#v, want active 1 limit 1", started[0].Capacity)
	}
	completed := finished[0]
	assertReplyOperation(t, completed.Operation, customerservice.ReplyModeGenerate, command)
	if completed.Outcome != customerservice.ReplyOutcomeCompleted ||
		completed.Phase != customerservice.ReplyPhasePersist ||
		completed.Capacity != (customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 1}) ||
		completed.Duplicate || completed.CleanupFailed {
		t.Fatalf("finished event = %#v", completed)
	}
	if completed.ModelDuration < 0 || completed.TotalDuration < completed.ModelDuration {
		t.Fatalf(
			"durations: model=%v total=%v, want 0 <= model <= total",
			completed.ModelDuration,
			completed.TotalDuration,
		)
	}

	duplicate, err := service.GenerateBotReply(context.Background(), command)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate GenerateBotReply() = %#v, error %v", duplicate, err)
	}
	started, rejected, finished = observer.snapshot()
	if len(started) != 1 || len(rejected) != 0 || len(finished) != 1 {
		t.Fatalf("pre-existing reply unexpectedly emitted AI events: %d/%d/%d", len(started), len(rejected), len(finished))
	}
}

func TestReplyObserverRecordsCapacityRejectionWithoutStartingGeneration(t *testing.T) {
	observer := &recordingReplyObserver{}
	generator := newBlockingReplyGenerator(1)
	defer func() {
		select {
		case <-generator.release:
		default:
			close(generator.release)
		}
	}()
	service := newObservedReplyService(t, generator, nil, observer, 1)
	first := receiveReplyConcurrencyMessage(t, service, "observer-capacity-first")
	second := receiveReplyConcurrencyMessage(t, service, "observer-capacity-second")

	firstOutcome := make(chan replyOutcome, 1)
	go func() {
		result, err := service.GenerateBotReply(context.Background(), first)
		firstOutcome <- replyOutcome{result: result, err: err}
	}()
	waitForGeneratorStart(t, generator.started)

	_, err := service.GenerateBotReply(context.Background(), second)
	if !errors.Is(err, customerservice.ErrReplyCapacityExceeded) {
		t.Fatalf("second GenerateBotReply() error = %v, want ErrReplyCapacityExceeded", err)
	}
	started, rejected, finished := observer.snapshot()
	if len(started) != 1 || len(rejected) != 1 || len(finished) != 0 {
		t.Fatalf("observer events before release = %d/%d/%d, want 1/1/0", len(started), len(rejected), len(finished))
	}
	assertReplyOperation(t, rejected[0].Operation, customerservice.ReplyModeGenerate, second)
	if rejected[0].Capacity != (customerservice.ReplyCapacitySnapshot{Active: 1, Limit: 1}) {
		t.Fatalf("rejected capacity = %#v, want active 1 limit 1", rejected[0].Capacity)
	}

	close(generator.release)
	if outcome := waitForReplyOutcome(t, firstOutcome); outcome.err != nil {
		t.Fatalf("first GenerateBotReply() error = %v", outcome.err)
	}
	started, rejected, finished = observer.snapshot()
	if len(started) != 1 || len(rejected) != 1 || len(finished) != 1 {
		t.Fatalf("observer events after release = %d/%d/%d, want 1/1/1", len(started), len(rejected), len(finished))
	}
}

func TestReplyObserverCoversStreamUntilTerminalOrClose(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		streamer := customerservice.ReplyStreamGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "streamed answer"}}), nil
		})
		service := newObservedReplyService(t, nil, streamer, observer, 1)
		command := receiveReplyConcurrencyMessage(t, service, "observer-stream-completed")

		reader, err := service.StreamBotReply(context.Background(), command)
		if err != nil {
			t.Fatalf("StreamBotReply() error = %v", err)
		}
		started, rejected, finished := observer.snapshot()
		if len(started) != 1 || len(rejected) != 0 || len(finished) != 0 {
			t.Fatalf("events after stream creation = %d/%d/%d, want 1/0/0", len(started), len(rejected), len(finished))
		}
		assertReplyOperation(t, started[0].Operation, customerservice.ReplyModeStream, command)

		delta, err := reader.Recv()
		if err != nil || delta.Type != customerservice.BotReplyStreamEventDelta {
			t.Fatalf("first Recv() = %#v, error %v; want delta", delta, err)
		}
		_, _, finished = observer.snapshot()
		if len(finished) != 0 {
			t.Fatalf("finished events after delta = %d, want 0", len(finished))
		}

		completed, err := reader.Recv()
		if err != nil || completed.Type != customerservice.BotReplyStreamEventCompleted {
			t.Fatalf("terminal Recv() = %#v, error %v; want completed", completed, err)
		}
		if _, err := reader.Recv(); !errors.Is(err, io.EOF) {
			t.Fatalf("Recv() after terminal error = %v, want io.EOF", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}

		_, _, finished = observer.snapshot()
		if len(finished) != 1 {
			t.Fatalf("finished events = %d, want exactly 1", len(finished))
		}
		event := finished[0]
		if event.Outcome != customerservice.ReplyOutcomeCompleted ||
			event.Phase != customerservice.ReplyPhasePersist ||
			event.Capacity != (customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 1}) {
			t.Fatalf("finished event = %#v", event)
		}
	})

	t.Run("early close", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		streamer := customerservice.ReplyStreamGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "temporary"}}), nil
		})
		service := newObservedReplyService(t, nil, streamer, observer, 1)
		command := receiveReplyConcurrencyMessage(t, service, "observer-stream-close")

		reader, err := service.StreamBotReply(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}

		started, rejected, finished := observer.snapshot()
		if len(started) != 1 || len(rejected) != 0 || len(finished) != 1 {
			t.Fatalf("observer events = %d/%d/%d, want 1/0/1", len(started), len(rejected), len(finished))
		}
		if finished[0].Outcome != customerservice.ReplyOutcomeAbandoned ||
			finished[0].Phase != customerservice.ReplyPhaseClose ||
			finished[0].Capacity != (customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 1}) {
			t.Fatalf("finished event = %#v", finished[0])
		}
	})
}

func TestReplyObserverPanicsNeverAffectBusinessOrCapacity(t *testing.T) {
	t.Run("started and finished", func(t *testing.T) {
		service := newObservedReplyService(
			t,
			customerservice.ReplyGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (customerservice.Reply, error) {
				return customerservice.Reply{Content: "answer"}, nil
			}),
			nil,
			panickingReplyObserver{},
			1,
		)
		first := receiveReplyConcurrencyMessage(t, service, "observer-panic-first")
		second := receiveReplyConcurrencyMessage(t, service, "observer-panic-second")

		if _, err := service.GenerateBotReply(context.Background(), first); err != nil {
			t.Fatalf("first GenerateBotReply() error = %v", err)
		}
		if _, err := service.GenerateBotReply(context.Background(), second); err != nil {
			t.Fatalf("second GenerateBotReply() error = %v; capacity may have leaked", err)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		generator := newBlockingReplyGenerator(1)
		service := newObservedReplyService(t, generator, nil, panickingReplyObserver{}, 1)
		first := receiveReplyConcurrencyMessage(t, service, "observer-reject-panic-first")
		second := receiveReplyConcurrencyMessage(t, service, "observer-reject-panic-second")
		outcome := make(chan replyOutcome, 1)
		go func() {
			result, err := service.GenerateBotReply(context.Background(), first)
			outcome <- replyOutcome{result: result, err: err}
		}()
		waitForGeneratorStart(t, generator.started)

		if _, err := service.GenerateBotReply(context.Background(), second); !errors.Is(
			err,
			customerservice.ErrReplyCapacityExceeded,
		) {
			t.Fatalf("capacity rejection error = %v, want ErrReplyCapacityExceeded", err)
		}
		close(generator.release)
		if result := waitForReplyOutcome(t, outcome); result.err != nil {
			t.Fatalf("first GenerateBotReply() error = %v", result.err)
		}
	})
}

func TestReplyAttemptFinishesAndReleasesCapacityAfterPanics(t *testing.T) {
	t.Run("provider Generate", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		var calls atomic.Int64
		service := newObservedReplyService(
			t,
			customerservice.ReplyGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (customerservice.Reply, error) {
				if calls.Add(1) == 1 {
					panic("provider panic must propagate")
				}
				return customerservice.Reply{Content: "recovered"}, nil
			}),
			nil,
			observer,
			1,
		)
		first := receiveReplyConcurrencyMessage(t, service, "provider-panic-first")
		second := receiveReplyConcurrencyMessage(t, service, "provider-panic-second")

		if recovered := capturePanic(func() {
			_, _ = service.GenerateBotReply(context.Background(), first)
		}); recovered == nil {
			t.Fatal("GenerateBotReply() provider panic was swallowed")
		}
		assertSingleFinishedEvent(
			t,
			observer,
			customerservice.ReplyOutcomeGenerationFailed,
			customerservice.ReplyPhaseGenerate,
			false,
		)
		if _, err := service.GenerateBotReply(context.Background(), second); err != nil {
			t.Fatalf("GenerateBotReply() after provider panic error = %v", err)
		}
	})

	t.Run("repository persistence", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		repository := &panicWithinTxRepository{
			Repository: conversation.NewMemoryRepository(),
			panicAt:    3,
		}
		service, err := customerservice.New(
			repository,
			&sequenceIDGenerator{},
			customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
			customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (customerservice.Reply, error) {
				return customerservice.Reply{Content: "answer"}, nil
			})),
			customerservice.WithReplyObserver(observer),
			customerservice.WithMaxConcurrentReplies(1),
		)
		if err != nil {
			t.Fatal(err)
		}
		command := receiveReplyConcurrencyMessage(t, service, "persistence-panic")

		if recovered := capturePanic(func() {
			_, _ = service.GenerateBotReply(context.Background(), command)
		}); recovered == nil {
			t.Fatal("GenerateBotReply() persistence panic was swallowed")
		}
		assertSingleFinishedEvent(
			t,
			observer,
			customerservice.ReplyOutcomePersistenceFailed,
			customerservice.ReplyPhasePersist,
			false,
		)
		if _, err := service.GenerateBotReply(context.Background(), command); err != nil {
			t.Fatalf("GenerateBotReply() after persistence panic error = %v", err)
		}
	})

	t.Run("stream Recv", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		streamer := customerservice.ReplyStreamGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
				panic("stream Recv panic must propagate")
			}, nil), nil
		})
		service := newObservedReplyService(t, nil, streamer, observer, 1)
		command := receiveReplyConcurrencyMessage(t, service, "stream-recv-panic")
		reader, err := service.StreamBotReply(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}

		if recovered := capturePanic(func() {
			_, _ = reader.Recv()
		}); recovered == nil {
			t.Fatal("stream Recv panic was swallowed")
		}
		assertSingleFinishedEvent(
			t,
			observer,
			customerservice.ReplyOutcomeGenerationFailed,
			customerservice.ReplyPhaseReceive,
			false,
		)
	})

	t.Run("stream Close", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		streamer := customerservice.ReplyStreamGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.NewReader(
				func() (customerservice.ReplyDelta, error) { return customerservice.ReplyDelta{}, io.EOF },
				func() error { panic("stream Close panic must propagate") },
			), nil
		})
		generator := customerservice.ReplyGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			return customerservice.Reply{Content: "capacity recovered"}, nil
		})
		service := newObservedReplyService(t, generator, streamer, observer, 1)
		streamCommand := receiveReplyConcurrencyMessage(t, service, "stream-close-panic")
		nextCommand := receiveReplyConcurrencyMessage(t, service, "stream-close-panic-next")
		reader, err := service.StreamBotReply(context.Background(), streamCommand)
		if err != nil {
			t.Fatal(err)
		}

		if recovered := capturePanic(func() { _ = reader.Close() }); recovered == nil {
			t.Fatal("stream Close panic was swallowed")
		}
		assertSingleFinishedEvent(
			t,
			observer,
			customerservice.ReplyOutcomeAbandoned,
			customerservice.ReplyPhaseClose,
			true,
		)
		if _, err := service.GenerateBotReply(context.Background(), nextCommand); err != nil {
			t.Fatalf("GenerateBotReply() after stream Close panic error = %v", err)
		}
	})
}

func TestReplyObserverPreservesPrimaryStreamOutcomeAndCleanupStatus(t *testing.T) {
	t.Run("initialization failure", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		providerErr := errors.New("provider initialization failed")
		streamer := customerservice.ReplyStreamGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.NewReader(
				func() (customerservice.ReplyDelta, error) { return customerservice.ReplyDelta{}, io.EOF },
				func() error { return context.Canceled },
			), providerErr
		})
		service := newObservedReplyService(t, nil, streamer, observer, 1)
		command := receiveReplyConcurrencyMessage(t, service, "stream-init-cleanup")

		reader, err := service.StreamBotReply(context.Background(), command)
		if reader != nil || !errors.Is(err, providerErr) || !errors.Is(err, context.Canceled) {
			t.Fatalf("StreamBotReply() reader = %#v, error = %v", reader, err)
		}
		assertSingleFinishedEvent(
			t,
			observer,
			customerservice.ReplyOutcomeGenerationFailed,
			customerservice.ReplyPhaseInitialize,
			true,
		)
	})

	t.Run("completed stream cleanup failure", func(t *testing.T) {
		observer := &recordingReplyObserver{}
		cleanupErr := errors.New("stream cleanup failed")
		index := 0
		streamer := customerservice.ReplyStreamGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
				if index == 0 {
					index++
					return customerservice.ReplyDelta{Content: "answer"}, nil
				}
				return customerservice.ReplyDelta{}, io.EOF
			}, func() error {
				return cleanupErr
			}), nil
		})
		service := newObservedReplyService(t, nil, streamer, observer, 1)
		command := receiveReplyConcurrencyMessage(t, service, "stream-terminal-cleanup")
		reader, err := service.StreamBotReply(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		if event, err := reader.Recv(); err != nil || event.Type != customerservice.BotReplyStreamEventDelta {
			t.Fatalf("delta Recv() = %#v, %v", event, err)
		}
		if event, err := reader.Recv(); err != nil || event.Type != customerservice.BotReplyStreamEventCompleted {
			t.Fatalf("completed Recv() = %#v, %v", event, err)
		}

		assertSingleFinishedEvent(
			t,
			observer,
			customerservice.ReplyOutcomeCompleted,
			customerservice.ReplyPhasePersist,
			true,
		)
		if err := reader.Close(); !errors.Is(err, cleanupErr) {
			t.Fatalf("stream Close() error = %v, want cleanup error", err)
		}
	})
}

type recordingReplyObserver struct {
	mu       sync.Mutex
	started  []customerservice.ReplyStartedEvent
	rejected []customerservice.ReplyRejectedEvent
	finished []customerservice.ReplyFinishedEvent
}

type panickingReplyObserver struct{}

func (panickingReplyObserver) ReplyStarted(context.Context, customerservice.ReplyStartedEvent) {
	panic("ReplyStarted observer panic")
}

func (panickingReplyObserver) ReplyRejected(context.Context, customerservice.ReplyRejectedEvent) {
	panic("ReplyRejected observer panic")
}

func (panickingReplyObserver) ReplyFinished(context.Context, customerservice.ReplyFinishedEvent) {
	panic("ReplyFinished observer panic")
}

type panicWithinTxRepository struct {
	conversation.Repository
	calls   atomic.Int64
	panicAt int64
}

func (r *panicWithinTxRepository) WithinTx(
	ctx context.Context,
	fn func(conversation.Transaction) error,
) error {
	if r.calls.Add(1) == r.panicAt {
		panic("repository persistence panic must propagate")
	}
	return r.Repository.WithinTx(ctx, fn)
}

func capturePanic(call func()) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	call()
	return nil
}

func assertSingleFinishedEvent(
	t *testing.T,
	observer *recordingReplyObserver,
	outcome customerservice.ReplyOutcome,
	phase customerservice.ReplyPhase,
	cleanupFailed bool,
) {
	t.Helper()
	_, _, finished := observer.snapshot()
	if len(finished) != 1 {
		t.Fatalf("finished events = %d, want 1: %#v", len(finished), finished)
	}
	event := finished[0]
	if event.Outcome != outcome || event.Phase != phase ||
		event.CleanupFailed != cleanupFailed ||
		event.Capacity != (customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 1}) {
		t.Fatalf(
			"finished event = %#v, want outcome=%q phase=%q cleanup=%v active=0/1",
			event,
			outcome,
			phase,
			cleanupFailed,
		)
	}
}

func (o *recordingReplyObserver) ReplyStarted(
	_ context.Context,
	event customerservice.ReplyStartedEvent,
) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, event)
}

func (o *recordingReplyObserver) ReplyRejected(
	_ context.Context,
	event customerservice.ReplyRejectedEvent,
) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rejected = append(o.rejected, event)
}

func (o *recordingReplyObserver) ReplyFinished(
	_ context.Context,
	event customerservice.ReplyFinishedEvent,
) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.finished = append(o.finished, event)
}

func (o *recordingReplyObserver) snapshot() (
	[]customerservice.ReplyStartedEvent,
	[]customerservice.ReplyRejectedEvent,
	[]customerservice.ReplyFinishedEvent,
) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]customerservice.ReplyStartedEvent(nil), o.started...),
		append([]customerservice.ReplyRejectedEvent(nil), o.rejected...),
		append([]customerservice.ReplyFinishedEvent(nil), o.finished...)
}

func newObservedReplyService(
	t *testing.T,
	generator customerservice.ReplyGenerator,
	streamer customerservice.ReplyStreamGenerator,
	observer customerservice.ReplyObserver,
	maxConcurrent int,
) *customerservice.Service {
	t.Helper()
	options := []customerservice.Option{
		customerservice.WithReplyObserver(observer),
		customerservice.WithMaxConcurrentReplies(maxConcurrent),
	}
	if generator != nil {
		options = append(options, customerservice.WithReplyGenerator(generator))
	}
	if streamer != nil {
		options = append(options, customerservice.WithReplyStreamGenerator(streamer))
	}
	service, err := customerservice.New(
		conversation.NewMemoryRepository(),
		&sequenceIDGenerator{},
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		options...,
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	return service
}

func assertReplyOperation(
	t *testing.T,
	operation customerservice.ReplyOperation,
	mode customerservice.ReplyMode,
	command customerservice.GenerateBotReplyCommand,
) {
	t.Helper()
	if operation.Mode != mode ||
		operation.ConversationID != command.ConversationID ||
		operation.TriggerMessageID != command.TriggerMessageID {
		t.Fatalf("reply operation = %#v, want mode %q and command %#v", operation, mode, command)
	}
}
