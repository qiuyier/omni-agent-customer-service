// Package integration implements a bounded, single-active adapter worker.
// It is separate from the customer-service application and is not a tenant
// authorization layer or a distributed queue.
package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const (
	MaxJobs         = 128
	MaxContentBytes = 16 << 10
	MaxReplyBytes   = 64 << 10
	maxRetryDelay   = time.Hour
)

var (
	ErrInvalidInput   = errors.New("integration: invalid input")
	ErrConflict       = errors.New("integration: event key reused with different data")
	ErrNotFound       = errors.New("integration: job not found")
	ErrFull           = errors.New("integration: journal capacity reached")
	ErrStateInUse     = errors.New("integration: state directory already in use")
	ErrStateInvalid   = errors.New("integration: incompatible or invalid state")
	ErrStore          = errors.New("integration: state storage failed; close and reopen before continuing")
	ErrClosed         = errors.New("integration: adapter closed")
	ErrNotReady       = errors.New("integration: retry is not due")
	ErrAttemptFailed  = errors.New("integration: attempt failed; progress retained")
	ErrNeedsAttention = errors.New("integration: job needs attention")
)

type State string

const (
	PendingReceive  State = "pending_receive"
	PendingReply    State = "pending_reply"
	PendingDelivery State = "pending_delivery"
	Completed       State = "completed"
	NeedsAttention  State = "needs_attention"
)

// Event identifiers are from one trusted source configured when opening the
// adapter. Content and all identifiers must remain identical on redelivery.
type Event struct {
	ConversationID    string `json:"conversation_id"`
	CustomerID        string `json:"customer_id"`
	Channel           string `json:"channel"`
	ExternalMessageID string `json:"external_message_id"`
	Content           string `json:"content"`
}

type Job struct {
	ID               string      `json:"id"`
	Event            Event       `json:"event"`
	State            State       `json:"state"`
	TriggerMessageID string      `json:"trigger_message_id,omitempty"`
	ReplyMessageID   string      `json:"reply_message_id,omitempty"`
	ReplyContent     string      `json:"reply_content,omitempty"`
	DeliveryKey      string      `json:"delivery_key,omitempty"`
	Attempts         int         `json:"attempts"`
	NextAttemptAt    time.Time   `json:"next_attempt_at"`
	LastFailure      FailureCode `json:"last_failure,omitempty"`
	AttentionFrom    State       `json:"attention_from,omitempty"`
}

// SourceID and DestinationID are trusted adapter configuration, never an
// identity accepted from an untrusted inbound message. Directory must be private.
type Config struct {
	Directory      string
	SourceID       string
	DestinationID  string
	MaxAttempts    int
	AttemptTimeout time.Duration
	RetryDelay     time.Duration
}

type Client interface {
	ReceiveMessage(context.Context, customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error)
	GenerateBotReply(context.Context, customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error)
}

type Delivery struct {
	Key               string
	SourceID          string
	DestinationID     string
	Channel           string
	ConversationID    string
	CustomerID        string
	ExternalMessageID string
	TriggerMessageID  string
	ReplyMessageID    string
	Content           string
}

// Deliver must respect context cancellation and return nil only when its
// delivery acknowledgement criterion is satisfied. Repeated Key values must
// be deduplicated by an idempotent destination. Non-idempotent destinations
// cannot obtain exactly-once delivery from this adapter.
type Deliverer interface {
	Deliver(context.Context, Delivery) error
}
type DelivererFunc func(context.Context, Delivery) error

func (f DelivererFunc) Deliver(ctx context.Context, d Delivery) error { return f(ctx, d) }

type FailureCode string

const (
	FailureTransport       FailureCode = "transport"
	FailureTimeout         FailureCode = "timeout"
	FailureCancelled       FailureCode = "cancelled"
	FailureInvalidRequest  FailureCode = "invalid_request"
	FailureUnauthorized    FailureCode = "unauthorized"
	FailureNotFound        FailureCode = "not_found"
	FailureConflict        FailureCode = "conflict"
	FailurePrecondition    FailureCode = "precondition"
	FailureBusy            FailureCode = "busy"
	FailureUnavailable     FailureCode = "unavailable"
	FailureInvalidResponse FailureCode = "invalid_response"
	FailureRetryBudget     FailureCode = "retry_budget"
	FailureManual          FailureCode = "manual_handling_required"
	FailureUnknown         FailureCode = "call_failed"
)

// CallError is the safe error vocabulary for client/delivery adapters. No URL,
// provider payload, credential or arbitrary error text enters the journal.
type CallError struct {
	Code       FailureCode
	Retryable  bool
	RetryAfter time.Duration
}

func (e *CallError) Error() string { return "integration: external call failed" }

func validFailure(code FailureCode) bool {
	switch code {
	case "", FailureTransport, FailureTimeout, FailureCancelled, FailureInvalidRequest,
		FailureUnauthorized, FailureNotFound, FailureConflict, FailurePrecondition,
		FailureBusy, FailureUnavailable, FailureInvalidResponse, FailureRetryBudget,
		FailureManual, FailureUnknown:
		return true
	default:
		return false
	}
}

func validID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) != ""
}
func validEvent(e Event) bool {
	return validID(e.ConversationID) && validID(e.CustomerID) && validID(e.Channel) &&
		validID(e.ExternalMessageID) && validText(e.Content, MaxContentBytes)
}

// Hash a versioned JSON tuple, not ambiguous concatenation. Prefixes and tuple
// layout form the persisted v1 mapping contract and must not change in place.
func mappedID(kind string, parts ...string) string {
	tuple := append([]string{"integration-v1", kind}, parts...)
	encoded, _ := json.Marshal(tuple)
	sum := sha256.Sum256(encoded)
	return "int1-" + hex.EncodeToString(sum[:])
}
func jobID(source string, e Event) string {
	return mappedID("job", source, e.Channel, e.ExternalMessageID)
}
func mapEvent(source string, e Event) customerservice.ReceiveMessageCommand {
	return customerservice.ReceiveMessageCommand{
		ConversationID:    mappedID("conversation", source, e.Channel, e.ConversationID),
		CustomerID:        mappedID("customer", source, e.CustomerID),
		Channel:           mappedID("channel", source, e.Channel),
		ExternalMessageID: mappedID("event", source, e.Channel, e.ExternalMessageID),
		Content:           e.Content,
	}
}
func deliveryKey(cfg Config, j Job) string {
	return mappedID("delivery", cfg.SourceID, cfg.DestinationID, j.Event.Channel,
		j.Event.CustomerID, j.ReplyMessageID)
}
