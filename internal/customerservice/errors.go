// Package customerservice contains application use cases that coordinate the
// conversation domain. It does not depend on HTTP, Eino, or a database.
package customerservice

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidInput indicates that an application command is incomplete.
	ErrInvalidInput = errors.New("customer service: invalid input")

	// ErrInvalidDependency indicates that the service was constructed without a
	// required dependency.
	ErrInvalidDependency = errors.New("customer service: invalid dependency")

	// ErrConversationMismatch indicates that a conversation ID belongs to a
	// different customer or channel.
	ErrConversationMismatch = errors.New("customer service: conversation mismatch")

	// ErrIdempotencyConflict indicates that an external message ID was reused
	// with different message data.
	ErrIdempotencyConflict = errors.New("customer service: idempotency conflict")

	// ErrInvalidReplyTrigger indicates that the selected trigger is not a
	// customer message in the requested conversation.
	ErrInvalidReplyTrigger = errors.New("customer service: invalid reply trigger")

	// ErrReplySuperseded indicates that the conversation changed while a reply
	// was being generated, so the generated content must be discarded.
	ErrReplySuperseded = errors.New("customer service: reply superseded")

	// ErrEmptyReply indicates that the model returned no usable text.
	ErrEmptyReply = errors.New("customer service: empty reply")

	// ErrReplyUnavailable indicates that this service instance has no usable
	// reply-generation capability configured.
	ErrReplyUnavailable = errors.New("customer service: reply unavailable")

	// ErrReplyGenerationFailed classifies a provider or model invocation
	// failure while preserving the original cause for errors.Is/errors.As.
	ErrReplyGenerationFailed = errors.New("customer service: reply generation failed")
)

func invalidInput(field, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidInput, field, reason)
}
