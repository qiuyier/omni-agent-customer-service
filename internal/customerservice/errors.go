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
)

func invalidInput(field, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidInput, field, reason)
}
