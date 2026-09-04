// Package observability contains concrete operational adapters. Business
// packages depend only on their own observer ports and never on Zap.
package observability

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

var errInvalidDependency = errors.New("observability: invalid dependency")

// RequestIDExtractor reads an optional request identifier without coupling
// this adapter to Hertz or any other transport.
type RequestIDExtractor func(context.Context) (string, bool)

// ZapReplyObserver writes bounded, structured reply lifecycle events.
type ZapReplyObserver struct {
	logger           *zap.Logger
	requestIDFromCtx RequestIDExtractor
}

func NewZapReplyObserver(
	logger *zap.Logger,
	requestIDFromCtx RequestIDExtractor,
) (*ZapReplyObserver, error) {
	if logger == nil {
		return nil, fmt.Errorf("%w: logger is nil", errInvalidDependency)
	}
	return &ZapReplyObserver{
		logger:           logger,
		requestIDFromCtx: requestIDFromCtx,
	}, nil
}

func (o *ZapReplyObserver) ReplyStarted(
	ctx context.Context,
	event customerservice.ReplyStartedEvent,
) {
	fields := o.baseFields(ctx, event.Operation, event.Capacity)
	fields = append(fields, zap.String("event", "ai.reply.started"))
	o.logger.Info("ai reply started", fields...)
}

func (o *ZapReplyObserver) ReplyRejected(
	ctx context.Context,
	event customerservice.ReplyRejectedEvent,
) {
	fields := o.baseFields(ctx, event.Operation, event.Capacity)
	fields = append(fields,
		zap.String("event", "ai.reply.rejected"),
		zap.String("outcome", "capacity_exceeded"),
	)
	o.logger.Warn("ai reply rejected", fields...)
}

func (o *ZapReplyObserver) ReplyFinished(
	ctx context.Context,
	event customerservice.ReplyFinishedEvent,
) {
	fields := o.baseFields(ctx, event.Operation, event.Capacity)
	fields = append(fields,
		zap.String("event", "ai.reply.finished"),
		zap.String("outcome", string(event.Outcome)),
		zap.String("terminal_phase", string(event.Phase)),
		zap.Float64("model_duration_ms", durationMilliseconds(event.ModelDuration)),
		zap.Float64("total_duration_ms", durationMilliseconds(event.TotalDuration)),
	)
	if event.Duplicate {
		fields = append(fields, zap.Bool("duplicate", true))
	}
	if event.CleanupFailed {
		fields = append(fields, zap.Bool("cleanup_failed", true))
	}

	switch {
	case event.Outcome == customerservice.ReplyOutcomeGenerationFailed,
		event.Outcome == customerservice.ReplyOutcomePersistenceFailed:
		o.logger.Error("ai reply finished", fields...)
	case event.CleanupFailed,
		event.Outcome == customerservice.ReplyOutcomeEmpty,
		event.Outcome == customerservice.ReplyOutcomeDeadlineExceeded:
		o.logger.Warn("ai reply finished", fields...)
	default:
		o.logger.Info("ai reply finished", fields...)
	}
}

func (o *ZapReplyObserver) baseFields(
	ctx context.Context,
	operation customerservice.ReplyOperation,
	capacity customerservice.ReplyCapacitySnapshot,
) []zap.Field {
	fields := make([]zap.Field, 0, 6)
	if o.requestIDFromCtx != nil {
		if requestID, ok := o.requestIDFromCtx(ctx); ok {
			fields = append(fields, zap.String("request_id", requestID))
		}
	}
	return append(fields,
		zap.String("reply_mode", string(operation.Mode)),
		zap.String("conversation_id", operation.ConversationID),
		zap.String("trigger_message_id", operation.TriggerMessageID),
		zap.Int("active_replies", capacity.Active),
		zap.Int("max_concurrent_replies", capacity.Limit),
	)
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration.Nanoseconds()) / 1e6
}

var _ customerservice.ReplyObserver = (*ZapReplyObserver)(nil)
