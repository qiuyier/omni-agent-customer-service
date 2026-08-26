package conversation

import (
	"fmt"
	"strings"
	"time"
)

// Status identifies who currently owns the right to reply in a conversation.
type Status string

const (
	StatusBotActive    Status = "bot_active"
	StatusWaitingHuman Status = "waiting_human"
	StatusHumanActive  Status = "human_active"
	StatusClosed       Status = "closed"
)

// Valid reports whether status is a known conversation status.
func (s Status) Valid() bool {
	switch s {
	case StatusBotActive, StatusWaitingHuman, StatusHumanActive, StatusClosed:
		return true
	default:
		return false
	}
}

// CreateParams contains the data required to start a conversation.
type CreateParams struct {
	ID         string
	CustomerID string
	Channel    string
	CreatedAt  time.Time
}

// RestoreParams contains the complete persisted state required to rehydrate a
// conversation. Callers must use Restore instead of constructing aggregate
// state directly so database data passes the same domain invariants.
type RestoreParams struct {
	ID         string
	CustomerID string
	Channel    string
	Status     Status
	AgentID    string
	Revision   uint64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Conversation is the aggregate root for a customer-service conversation.
// Fields are private so status changes must pass through domain methods.
type Conversation struct {
	id         string
	customerID string
	channel    string
	status     Status
	agentID    string
	revision   uint64
	createdAt  time.Time
	updatedAt  time.Time
}

// New creates a bot-owned conversation.
func New(params CreateParams) (*Conversation, error) {
	id, err := requiredString("id", params.ID)
	if err != nil {
		return nil, err
	}
	customerID, err := requiredString("customer_id", params.CustomerID)
	if err != nil {
		return nil, err
	}
	channel, err := requiredString("channel", params.Channel)
	if err != nil {
		return nil, err
	}
	createdAt, err := requiredTime("created_at", params.CreatedAt)
	if err != nil {
		return nil, err
	}

	return &Conversation{
		id:         id,
		customerID: customerID,
		channel:    channel,
		status:     StatusBotActive,
		revision:   1,
		createdAt:  createdAt,
		updatedAt:  createdAt,
	}, nil
}

// Restore rehydrates a conversation from persisted state after validating all
// aggregate invariants. It does not perform a state transition or increment
// the revision.
func Restore(params RestoreParams) (*Conversation, error) {
	id, err := requiredString("id", params.ID)
	if err != nil {
		return nil, err
	}
	customerID, err := requiredString("customer_id", params.CustomerID)
	if err != nil {
		return nil, err
	}
	channel, err := requiredString("channel", params.Channel)
	if err != nil {
		return nil, err
	}
	status := Status(strings.TrimSpace(string(params.Status)))
	createdAt, err := requiredTime("created_at", params.CreatedAt)
	if err != nil {
		return nil, err
	}
	updatedAt, err := requiredTime("updated_at", params.UpdatedAt)
	if err != nil {
		return nil, err
	}

	current := &Conversation{
		id:         id,
		customerID: customerID,
		channel:    channel,
		status:     status,
		agentID:    strings.TrimSpace(params.AgentID),
		revision:   params.Revision,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}
	if err := validateConversation(current); err != nil {
		return nil, err
	}
	return current, nil
}

// RequestHandoff moves a bot-owned conversation into the human queue.
func (c *Conversation) RequestHandoff(now time.Time) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if c.status != StatusBotActive {
		return invalidTransition("request handoff", c.status)
	}
	updatedAt, err := c.transitionTime(now)
	if err != nil {
		return err
	}

	c.status = StatusWaitingHuman
	c.agentID = ""
	c.revision++
	c.updatedAt = updatedAt
	return nil
}

// CancelHandoff returns a queued conversation to the bot.
func (c *Conversation) CancelHandoff(now time.Time) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if c.status != StatusWaitingHuman {
		return invalidTransition("cancel handoff", c.status)
	}
	updatedAt, err := c.transitionTime(now)
	if err != nil {
		return err
	}

	c.status = StatusBotActive
	c.agentID = ""
	c.revision++
	c.updatedAt = updatedAt
	return nil
}

// AssignAgent transfers reply ownership to a human agent.
func (c *Conversation) AssignAgent(agentID string, now time.Time) error {
	agentID, err := requiredString("agent_id", agentID)
	if err != nil {
		return err
	}
	if err := c.ensureOpen(); err != nil {
		return err
	}
	if c.status != StatusWaitingHuman {
		return invalidTransition("assign agent", c.status)
	}
	updatedAt, err := c.transitionTime(now)
	if err != nil {
		return err
	}

	c.status = StatusHumanActive
	c.agentID = agentID
	c.revision++
	c.updatedAt = updatedAt
	return nil
}

// Close permanently closes a conversation.
func (c *Conversation) Close(now time.Time) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	updatedAt, err := c.transitionTime(now)
	if err != nil {
		return err
	}

	c.status = StatusClosed
	c.revision++
	c.updatedAt = updatedAt
	return nil
}

// CanAcceptMessage verifies whether sender owns permission to write in the
// current state.
func (c *Conversation) CanAcceptMessage(sender Sender) error {
	if !sender.Valid() {
		return invalidArgument("sender", fmt.Sprintf("%q is unknown", sender))
	}
	if err := c.ensureOpen(); err != nil {
		return err
	}

	allowed := false
	switch c.status {
	case StatusBotActive:
		allowed = sender == SenderCustomer ||
			sender == SenderBot ||
			sender == SenderSystem ||
			sender == SenderTool
	case StatusWaitingHuman:
		allowed = sender == SenderCustomer ||
			sender == SenderSystem
	case StatusHumanActive:
		allowed = sender == SenderCustomer ||
			sender == SenderHumanAgent ||
			sender == SenderSystem
	}
	if !allowed {
		return senderNotAllowed(sender, c.status)
	}
	return nil
}

// ID returns the conversation identifier.
func (c *Conversation) ID() string { return c.id }

// CustomerID returns the customer identifier.
func (c *Conversation) CustomerID() string { return c.customerID }

// Channel returns the source channel.
func (c *Conversation) Channel() string { return c.channel }

// Status returns the current conversation status.
func (c *Conversation) Status() Status { return c.status }

// AgentID returns the assigned human agent identifier, if any.
func (c *Conversation) AgentID() string { return c.agentID }

// Revision returns the monotonic aggregate version. It changes on every state
// transition, even when a later transition returns to the same status.
func (c *Conversation) Revision() uint64 { return c.revision }

// CreatedAt returns the creation time in UTC.
func (c *Conversation) CreatedAt() time.Time { return c.createdAt }

// UpdatedAt returns the most recent state-change time in UTC.
func (c *Conversation) UpdatedAt() time.Time { return c.updatedAt }

func (c *Conversation) ensureOpen() error {
	if c == nil {
		return invalidArgument("conversation", "is nil")
	}
	if c.status == StatusClosed {
		return ErrConversationClosed
	}
	return nil
}

func (c *Conversation) transitionTime(now time.Time) (time.Time, error) {
	normalized, err := requiredTime("transition_time", now)
	if err != nil {
		return time.Time{}, err
	}
	if normalized.Before(c.updatedAt) {
		return time.Time{}, invalidArgument("transition_time", "must not be before updated_at")
	}
	return normalized, nil
}

func requiredString(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalidArgument(field, "is required")
	}
	return value, nil
}

func requiredTime(field string, value time.Time) (time.Time, error) {
	if value.IsZero() {
		return time.Time{}, invalidArgument(field, "is required")
	}
	return value.UTC(), nil
}

func validateConversation(current *Conversation) error {
	if current == nil {
		return invalidArgument("conversation", "is nil")
	}
	if _, err := requiredString("id", current.id); err != nil {
		return err
	}
	if _, err := requiredString("customer_id", current.customerID); err != nil {
		return err
	}
	if _, err := requiredString("channel", current.channel); err != nil {
		return err
	}
	if !current.status.Valid() {
		return invalidArgument("status", fmt.Sprintf("%q is unknown", current.status))
	}
	if _, err := requiredTime("created_at", current.createdAt); err != nil {
		return err
	}
	if _, err := requiredTime("updated_at", current.updatedAt); err != nil {
		return err
	}
	if current.updatedAt.Before(current.createdAt) {
		return invalidArgument("updated_at", "must not be before created_at")
	}
	if current.revision == 0 {
		return invalidArgument("revision", "must be greater than zero")
	}
	if current.revision > uint64(1<<63-1) {
		return invalidArgument("revision", "must fit in a signed 64-bit integer")
	}
	if current.status == StatusHumanActive && current.agentID == "" {
		return invalidArgument("agent_id", "is required while status is human_active")
	}
	if (current.status == StatusBotActive || current.status == StatusWaitingHuman) && current.agentID != "" {
		return invalidArgument("agent_id", fmt.Sprintf("must be empty while status is %s", current.status))
	}
	return nil
}
