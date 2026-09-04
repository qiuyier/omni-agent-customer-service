package customerservice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ReplyMode identifies the application reply path without exposing any model
// provider or transport type.
type ReplyMode string

const (
	ReplyModeGenerate ReplyMode = "generate"
	ReplyModeStream   ReplyMode = "stream"
)

// ReplyOutcome is a bounded, non-sensitive result category suitable for logs
// and metrics. It deliberately never contains an error string.
type ReplyOutcome string

const (
	ReplyOutcomeCompleted         ReplyOutcome = "completed"
	ReplyOutcomeSuperseded        ReplyOutcome = "superseded"
	ReplyOutcomeEmpty             ReplyOutcome = "empty"
	ReplyOutcomeGenerationFailed  ReplyOutcome = "generation_failed"
	ReplyOutcomePersistenceFailed ReplyOutcome = "persistence_failed"
	ReplyOutcomeCanceled          ReplyOutcome = "canceled"
	ReplyOutcomeDeadlineExceeded  ReplyOutcome = "deadline_exceeded"
	ReplyOutcomeAbandoned         ReplyOutcome = "abandoned"
)

// ReplyPhase identifies where a reply attempt reached its terminal outcome.
type ReplyPhase string

const (
	ReplyPhaseInitialize ReplyPhase = "initialize"
	ReplyPhaseGenerate   ReplyPhase = "generate"
	ReplyPhaseReceive    ReplyPhase = "receive"
	ReplyPhaseValidate   ReplyPhase = "validate"
	ReplyPhasePersist    ReplyPhase = "persist"
	ReplyPhaseClose      ReplyPhase = "close"
)

// ReplyOperation contains only stable identifiers. Conversation content,
// prompts, generated text, credentials, and provider errors are intentionally
// absent so an observer cannot accidentally log them.
type ReplyOperation struct {
	Mode             ReplyMode
	ConversationID   string
	TriggerMessageID string
}

// ReplyCapacitySnapshot is an exact process-local capacity snapshot captured
// while the limiter lock is held. A zero Limit means no limiter was configured.
type ReplyCapacitySnapshot struct {
	Active int
	Limit  int
}

type ReplyStartedEvent struct {
	Operation ReplyOperation
	Capacity  ReplyCapacitySnapshot
}

type ReplyRejectedEvent struct {
	Operation ReplyOperation
	Capacity  ReplyCapacitySnapshot
}

type ReplyFinishedEvent struct {
	Operation     ReplyOperation
	Outcome       ReplyOutcome
	Phase         ReplyPhase
	Capacity      ReplyCapacitySnapshot
	ModelDuration time.Duration
	TotalDuration time.Duration
	Duplicate     bool
	CleanupFailed bool
}

// ReplyObserver receives application-level reply lifecycle events. Methods
// must be concurrency-safe and return promptly; observability must never be
// used to make business decisions.
type ReplyObserver interface {
	ReplyStarted(context.Context, ReplyStartedEvent)
	ReplyRejected(context.Context, ReplyRejectedEvent)
	ReplyFinished(context.Context, ReplyFinishedEvent)
}

// WithReplyObserver installs a transport- and logger-independent observer.
func WithReplyObserver(observer ReplyObserver) Option {
	return func(service *Service) error {
		if observer == nil {
			return fmt.Errorf("%w: reply observer is nil", ErrInvalidDependency)
		}
		service.replyObserver = observer
		return nil
	}
}

type nopReplyObserver struct{}

func (nopReplyObserver) ReplyStarted(context.Context, ReplyStartedEvent)   {}
func (nopReplyObserver) ReplyRejected(context.Context, ReplyRejectedEvent) {}
func (nopReplyObserver) ReplyFinished(context.Context, ReplyFinishedEvent) {}

type replyAttempt struct {
	ctx                 context.Context
	observer            ReplyObserver
	operation           ReplyOperation
	startedAt           time.Time
	generationStartedAt time.Time
	lease               *replyCapacityLease

	generationOnce   sync.Once
	finishOnce       sync.Once
	modelDuration    time.Duration
	releasedCapacity ReplyCapacitySnapshot
}

func (s *Service) beginReplyAttempt(
	ctx context.Context,
	mode ReplyMode,
	request ReplyRequest,
) (*replyAttempt, error) {
	operation := ReplyOperation{
		Mode:             mode,
		ConversationID:   request.ConversationID,
		TriggerMessageID: request.TriggerMessageID,
	}
	lease, capacity, err := s.acquireReplyCapacity()
	if err != nil {
		observeReplyEvent(func() {
			s.replyObserver.ReplyRejected(ctx, ReplyRejectedEvent{
				Operation: operation,
				Capacity:  capacity,
			})
		})
		return nil, err
	}

	attempt := &replyAttempt{
		ctx:       ctx,
		observer:  s.replyObserver,
		operation: operation,
		startedAt: time.Now(),
		lease:     lease,
	}
	observeReplyEvent(func() {
		attempt.observer.ReplyStarted(ctx, ReplyStartedEvent{
			Operation: operation,
			Capacity:  capacity,
		})
	})
	attempt.generationStartedAt = time.Now()
	return attempt, nil
}

func (a *replyAttempt) generationDone() ReplyCapacitySnapshot {
	if a == nil {
		return ReplyCapacitySnapshot{}
	}
	a.generationOnce.Do(func() {
		a.modelDuration = time.Since(a.generationStartedAt)
		a.releasedCapacity = a.lease.release()
	})
	return a.releasedCapacity
}

func (a *replyAttempt) finish(
	outcome ReplyOutcome,
	phase ReplyPhase,
	duplicate bool,
	cleanupFailed bool,
) {
	if a == nil {
		return
	}
	a.finishOnce.Do(func() {
		capacity := a.generationDone()
		observeReplyEvent(func() {
			a.observer.ReplyFinished(a.ctx, ReplyFinishedEvent{
				Operation:     a.operation,
				Outcome:       outcome,
				Phase:         phase,
				Capacity:      capacity,
				ModelDuration: a.modelDuration,
				TotalDuration: time.Since(a.startedAt),
				Duplicate:     duplicate,
				CleanupFailed: cleanupFailed,
			})
		})
	})
}

// Observability is best-effort. A broken observer must not fail a customer
// request or leak an acquired model-generation slot.
func observeReplyEvent(notify func()) {
	defer func() {
		_ = recover()
	}()
	notify()
}

func replyOutcomeFromError(err error, fallback ReplyOutcome) ReplyOutcome {
	switch {
	case errors.Is(err, context.Canceled):
		return ReplyOutcomeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ReplyOutcomeDeadlineExceeded
	case errors.Is(err, ErrReplySuperseded):
		return ReplyOutcomeSuperseded
	case errors.Is(err, ErrEmptyReply):
		return ReplyOutcomeEmpty
	default:
		return fallback
	}
}
