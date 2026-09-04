package hertz

import (
	"context"
	"errors"
	"fmt"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-kit/fault"
)

var errInvalidDependency = errors.New("hertz transport: invalid dependency")

func invalidDependency(reason string) error {
	return fmt.Errorf("%w: %s", errInvalidDependency, reason)
}

// classifyError is the single boundary between internal errors and public
// client-safe error categories. Safe messages are deliberately generic; the
// wrapped cause remains available to errors.Is/errors.As and server logs.
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, context.Canceled):
		return fault.Wrap(fault.CodeCancelled, "request cancelled", err)

	case errors.Is(err, context.DeadlineExceeded):
		return fault.Wrap(fault.CodeDeadlineExceeded, "deadline exceeded", err)

	case errors.Is(err, customerservice.ErrInvalidInput),
		errors.Is(err, customerservice.ErrInvalidReplyTrigger),
		errors.Is(err, conversation.ErrInvalidArgument):
		return fault.Wrap(fault.CodeInvalidArgument, "invalid request", err)

	case errors.Is(err, conversation.ErrConversationNotFound),
		errors.Is(err, conversation.ErrMessageNotFound),
		errors.Is(err, conversation.ErrReplyNotFound):
		return fault.Wrap(fault.CodeNotFound, "resource not found", err)

	case errors.Is(err, customerservice.ErrConversationMismatch),
		errors.Is(err, customerservice.ErrIdempotencyConflict),
		errors.Is(err, customerservice.ErrReplySuperseded),
		errors.Is(err, conversation.ErrConversationAlreadyExists),
		errors.Is(err, conversation.ErrMessageAlreadyExists),
		errors.Is(err, conversation.ErrDuplicateExternalMessage),
		errors.Is(err, conversation.ErrReplyAlreadyExists):
		return fault.Wrap(fault.CodeConflict, "resource conflict", err)

	case errors.Is(err, conversation.ErrInvalidTransition),
		errors.Is(err, conversation.ErrConversationClosed),
		errors.Is(err, conversation.ErrSenderNotAllowed):
		return fault.Wrap(fault.CodeFailedPrecondition, "failed precondition", err)

	case errors.Is(err, customerservice.ErrInvalidDependency):
		return fault.Wrap(fault.CodeInternal, "internal error", err)

	case errors.Is(err, customerservice.ErrReplyCapacityExceeded):
		return fault.Wrap(fault.CodeResourceExhausted, "reply capacity exhausted", err)

	case errors.Is(err, customerservice.ErrReplyUnavailable),
		errors.Is(err, customerservice.ErrReplyGenerationFailed),
		errors.Is(err, customerservice.ErrEmptyReply):
		return fault.Wrap(fault.CodeUnavailable, "reply service unavailable", err)
	}

	var classified *fault.Error
	if errors.As(err, &classified) {
		return err
	}

	// problem.Write safely turns unclassified errors into an internal fault
	// without exposing err.Error() to the client.
	return err
}

func classifyStreamTerminalError(ctx context.Context, err error) error {
	if ctx != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return classifyError(contextErr)
		}
	}
	return classifyError(err)
}
