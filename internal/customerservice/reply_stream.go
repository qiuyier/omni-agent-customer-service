package customerservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

// ReplyDelta is one provider-independent text fragment produced by a model.
type ReplyDelta struct {
	Content string
}

// ReplyStreamGenerator is the streaming AI port required by the application.
// It deliberately exposes the kit's neutral pull-based Reader instead of Eino
// or Hertz types.
type ReplyStreamGenerator interface {
	Stream(ctx context.Context, request ReplyRequest) (kitstream.Reader[ReplyDelta], error)
}

// ReplyStreamGeneratorFunc adapts a function to ReplyStreamGenerator.
type ReplyStreamGeneratorFunc func(
	context.Context,
	ReplyRequest,
) (kitstream.Reader[ReplyDelta], error)

// Stream calls the underlying function.
func (f ReplyStreamGeneratorFunc) Stream(
	ctx context.Context,
	request ReplyRequest,
) (kitstream.Reader[ReplyDelta], error) {
	return f(ctx, request)
}

// WithReplyStreamGenerator enables streaming AI replies independently from
// the non-streaming ReplyGenerator capability.
func WithReplyStreamGenerator(generator ReplyStreamGenerator) Option {
	return func(service *Service) error {
		if generator == nil {
			return fmt.Errorf("%w: reply stream generator is nil", ErrInvalidDependency)
		}
		service.replyStreams = generator
		return nil
	}
}

// BotReplyStreamEventType identifies one application-level stream event.
type BotReplyStreamEventType string

const (
	BotReplyStreamEventDelta      BotReplyStreamEventType = "delta"
	BotReplyStreamEventCompleted  BotReplyStreamEventType = "completed"
	BotReplyStreamEventSuperseded BotReplyStreamEventType = "superseded"
)

// BotReplyStreamEvent is independent from SSE. Result is populated for
// terminal events; only Delta is populated for incremental text events.
type BotReplyStreamEvent struct {
	Type   BotReplyStreamEventType
	Delta  string
	Result GenerateBotReplyResult
}

// StreamBotReply streams temporary text deltas and emits a completed event
// only after the final content passes the same persistence checks as the
// non-streaming use case. A superseded event means prior deltas were not saved.
func (s *Service) StreamBotReply(
	ctx context.Context,
	command GenerateBotReplyCommand,
) (kitstream.Reader[BotReplyStreamEvent], error) {
	if ctx == nil {
		return nil, invalidInput("context", "is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	normalized, err := normalizeGenerateBotReplyCommand(command)
	if err != nil {
		return nil, err
	}
	prepared, err := s.prepareBotReply(ctx, normalized)
	if err != nil {
		return nil, err
	}
	if prepared.existing != nil {
		return kitstream.FromSlice([]BotReplyStreamEvent{{
			Type:   BotReplyStreamEventCompleted,
			Result: *prepared.existing,
		}}), nil
	}
	if s.replyStreams == nil {
		return nil, fmt.Errorf(
			"%w: reply stream generator is not configured",
			ErrReplyUnavailable,
		)
	}
	prepared.snapshot.request, err = s.prepareReplyKnowledge(ctx, prepared.snapshot.request)
	if err != nil {
		return nil, err
	}
	attempt, err := s.beginReplyAttempt(ctx, ReplyModeStream, prepared.snapshot.request)
	if err != nil {
		return nil, err
	}
	generationContext, cancelGeneration := s.newReplyGenerationContext(ctx)
	capacityTransferred := false
	initializationOutcome := ReplyOutcomeGenerationFailed
	initializationCleanupFailed := false
	defer func() {
		if !capacityTransferred {
			cancelGeneration()
			attempt.finish(
				initializationOutcome,
				ReplyPhaseInitialize,
				false,
				initializationCleanupFailed,
			)
		}
	}()

	source, err := s.replyStreams.Stream(generationContext, prepared.snapshot.request)
	if generationErr := generationContext.Err(); generationErr != nil {
		err = generationErr
	}
	if err != nil {
		// Classify the primary initialization failure before joining cleanup
		// errors. A Close error such as context.Canceled must not overwrite the
		// real provider outcome.
		initializationOutcome = replyOutcomeFromError(err, ReplyOutcomeGenerationFailed)
		cancelGeneration()
		if source != nil {
			// Pessimistically mark cleanup failed so a Close panic is still
			// represented by the outer lifecycle fallback.
			initializationCleanupFailed = true
			closeErr := source.Close()
			initializationCleanupFailed = closeErr != nil
			err = errors.Join(err, closeErr)
		}
		attempt.finish(
			initializationOutcome,
			ReplyPhaseInitialize,
			false,
			initializationCleanupFailed,
		)
		return nil, replyGenerationError(err)
	}
	if source == nil {
		cancelGeneration()
		attempt.finish(
			ReplyOutcomeGenerationFailed,
			ReplyPhaseInitialize,
			false,
			false,
		)
		return nil, replyGenerationError(errors.New("reply stream generator returned nil reader"))
	}

	state := &botReplyStreamState{
		requestContext:    ctx,
		generationContext: generationContext,
		cancelGeneration:  cancelGeneration,
		source:            source,
		service:           s,
		command:           normalized,
		snapshot:          prepared.snapshot,
		attempt:           attempt,
	}
	reader := kitstream.NewReader(state.recv, state.close)
	capacityTransferred = true
	return reader, nil
}

type botReplyStreamState struct {
	requestContext    context.Context
	generationContext context.Context
	cancelGeneration  context.CancelFunc
	source            kitstream.Reader[ReplyDelta]
	service           *Service
	command           GenerateBotReplyCommand
	snapshot          replySnapshot
	attempt           *replyAttempt
	content           strings.Builder
	done              bool
}

func (s *botReplyStreamState) recv() (BotReplyStreamEvent, error) {
	panicOutcome := ReplyOutcomeGenerationFailed
	panicPhase := ReplyPhaseReceive
	defer func() {
		if recovered := recover(); recovered != nil {
			s.done = true
			s.cancelGeneration()
			cleanupFailed := closeReplySourceAfterPanic(s.source)
			s.attempt.finish(panicOutcome, panicPhase, false, cleanupFailed)
			panic(recovered)
		}
	}()

	if s.done {
		return BotReplyStreamEvent{}, io.EOF
	}

	for {
		delta, err := s.source.Recv()
		switch {
		case err == nil:
			if delta.Content == "" {
				continue
			}
			s.content.WriteString(delta.Content)
			return BotReplyStreamEvent{
				Type:  BotReplyStreamEventDelta,
				Delta: delta.Content,
			}, nil

		case errors.Is(err, io.EOF):
			s.done = true
			generationErr := s.generationContext.Err()
			closeErr := s.finishGeneration(ReplyOutcomeGenerationFailed, ReplyPhaseClose)
			cleanupFailed := closeErr != nil
			if generationErr != nil {
				s.attempt.finish(
					replyOutcomeFromError(generationErr, ReplyOutcomeGenerationFailed),
					ReplyPhaseReceive,
					false,
					cleanupFailed,
				)
				return BotReplyStreamEvent{}, replyGenerationError(generationErr)
			}
			panicPhase = ReplyPhaseValidate
			content := s.content.String()
			if strings.TrimSpace(content) == "" {
				s.attempt.finish(ReplyOutcomeEmpty, ReplyPhaseValidate, false, cleanupFailed)
				return BotReplyStreamEvent{}, ErrEmptyReply
			}
			panicOutcome = ReplyOutcomePersistenceFailed
			panicPhase = ReplyPhasePersist
			result, persistErr := s.service.persistBotReply(
				s.requestContext,
				s.command,
				s.snapshot,
				content,
			)
			if errors.Is(persistErr, ErrReplySuperseded) {
				s.attempt.finish(ReplyOutcomeSuperseded, ReplyPhasePersist, false, cleanupFailed)
				return BotReplyStreamEvent{
					Type: BotReplyStreamEventSuperseded,
					Result: GenerateBotReplyResult{
						ConversationID:   s.command.ConversationID,
						TriggerMessageID: s.command.TriggerMessageID,
					},
				}, nil
			}
			if persistErr != nil {
				s.attempt.finish(
					replyOutcomeFromError(persistErr, ReplyOutcomePersistenceFailed),
					ReplyPhasePersist,
					false,
					cleanupFailed,
				)
				return BotReplyStreamEvent{}, persistErr
			}
			s.attempt.finish(
				ReplyOutcomeCompleted,
				ReplyPhasePersist,
				result.Duplicate,
				cleanupFailed,
			)
			return BotReplyStreamEvent{
				Type:   BotReplyStreamEventCompleted,
				Result: result,
			}, nil

		default:
			s.done = true
			generationErr := s.generationContext.Err()
			closeErr := s.finishGeneration(ReplyOutcomeGenerationFailed, ReplyPhaseClose)
			if generationErr != nil {
				err = generationErr
			}
			s.attempt.finish(
				replyOutcomeFromError(err, ReplyOutcomeGenerationFailed),
				ReplyPhaseReceive,
				false,
				closeErr != nil,
			)
			return BotReplyStreamEvent{}, replyGenerationError(err)
		}
	}
}

func (s *botReplyStreamState) close() error {
	requestErr := s.requestContext.Err()
	generationErr := s.generationContext.Err()

	outcome := ReplyOutcomeAbandoned
	if generationErr != nil {
		outcome = replyOutcomeFromError(generationErr, ReplyOutcomeAbandoned)
	} else if requestErr != nil {
		outcome = replyOutcomeFromError(requestErr, ReplyOutcomeAbandoned)
	}
	closeErr := s.finishGeneration(outcome, ReplyPhaseClose)
	s.attempt.finish(outcome, ReplyPhaseClose, false, closeErr != nil)
	return closeErr
}

func (s *botReplyStreamState) finishGeneration(
	panicOutcome ReplyOutcome,
	panicPhase ReplyPhase,
) (closeErr error) {
	closeReturned := false
	defer func() {
		s.attempt.generationDone()
		if !closeReturned {
			s.attempt.finish(panicOutcome, panicPhase, false, true)
		}
	}()
	s.cancelGeneration()
	closeErr = s.source.Close()
	closeReturned = true
	return closeErr
}

func closeReplySourceAfterPanic(source kitstream.Reader[ReplyDelta]) (cleanupFailed bool) {
	cleanupFailed = true
	defer func() {
		_ = recover()
	}()
	return source.Close() != nil
}
