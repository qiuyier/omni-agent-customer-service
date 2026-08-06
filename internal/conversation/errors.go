package conversation

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidArgument indicates invalid data supplied to a domain operation.
	ErrInvalidArgument = errors.New("conversation: invalid argument")

	// ErrInvalidTransition indicates that an action is not allowed in the
	// conversation's current state.
	ErrInvalidTransition = errors.New("conversation: invalid state transition")

	// ErrConversationClosed indicates that a closed conversation is immutable.
	ErrConversationClosed = errors.New("conversation: already closed")

	// ErrSenderNotAllowed indicates that the sender cannot write in the current
	// conversation state.
	ErrSenderNotAllowed = errors.New("conversation: sender not allowed")
)

func invalidArgument(field, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrInvalidArgument, field, reason)
}

func invalidTransition(action string, from Status) error {
	return fmt.Errorf("%w: cannot %s from %q", ErrInvalidTransition, action, from)
}

func senderNotAllowed(sender Sender, status Status) error {
	return fmt.Errorf("%w: sender %q cannot write while status is %q", ErrSenderNotAllowed, sender, status)
}
