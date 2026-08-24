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

	generationContext, cancelGeneration := s.newReplyGenerationContext(ctx)
	source, err := s.replyStreams.Stream(generationContext, prepared.snapshot.request)
	if generationErr := generationContext.Err(); generationErr != nil {
		err = generationErr
	}
	if err != nil {
		if source != nil {
			err = errors.Join(err, source.Close())
		}
		cancelGeneration()
		return nil, replyGenerationError(err)
	}
	if source == nil {
		cancelGeneration()
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
	}
	return kitstream.NewReader(state.recv, state.close), nil
}

type botReplyStreamState struct {
	requestContext    context.Context
	generationContext context.Context
	cancelGeneration  context.CancelFunc
	source            kitstream.Reader[ReplyDelta]
	service           *Service
	command           GenerateBotReplyCommand
	snapshot          replySnapshot
	content           strings.Builder
	done              bool
}

func (s *botReplyStreamState) recv() (BotReplyStreamEvent, error) {
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
			if generationErr := s.generationContext.Err(); generationErr != nil {
				return BotReplyStreamEvent{}, replyGenerationError(generationErr)
			}
			content := s.content.String()
			if strings.TrimSpace(content) == "" {
				return BotReplyStreamEvent{}, ErrEmptyReply
			}
			result, persistErr := s.service.persistBotReply(
				s.requestContext,
				s.command,
				s.snapshot,
				content,
			)
			if errors.Is(persistErr, ErrReplySuperseded) {
				return BotReplyStreamEvent{
					Type: BotReplyStreamEventSuperseded,
					Result: GenerateBotReplyResult{
						ConversationID:   s.command.ConversationID,
						TriggerMessageID: s.command.TriggerMessageID,
					},
				}, nil
			}
			if persistErr != nil {
				return BotReplyStreamEvent{}, persistErr
			}
			return BotReplyStreamEvent{
				Type:   BotReplyStreamEventCompleted,
				Result: result,
			}, nil

		default:
			s.done = true
			if generationErr := s.generationContext.Err(); generationErr != nil {
				err = generationErr
			}
			return BotReplyStreamEvent{}, replyGenerationError(err)
		}
	}
}

func (s *botReplyStreamState) close() error {
	s.cancelGeneration()
	return s.source.Close()
}
