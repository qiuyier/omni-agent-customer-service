package conversation

import (
	"context"
	"fmt"
	"sync"
)

type externalMessageKey struct {
	channel string
	id      string
}

// MemoryRepository is a concurrency-safe, transactional in-memory Repository.
// It is intended for tests and local development, not durable production data.
type MemoryRepository struct {
	mu sync.RWMutex

	conversations       map[string]*Conversation
	messages            map[string]Message
	messageIDsByConv    map[string][]string
	messageIDByExternal map[externalMessageKey]string
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		conversations:       make(map[string]*Conversation),
		messages:            make(map[string]Message),
		messageIDsByConv:    make(map[string][]string),
		messageIDByExternal: make(map[externalMessageKey]string),
	}
}

// FindConversation returns a detached copy so callers cannot mutate repository
// state without using a transaction.
func (r *MemoryRepository) FindConversation(ctx context.Context, id string) (*Conversation, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	id, err := requiredString("id", id)
	if err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return findConversation(r.conversations, id)
}

// FindMessageByExternalID finds a message using a channel-scoped idempotency
// key.
func (r *MemoryRepository) FindMessageByExternalID(
	ctx context.Context,
	channel string,
	externalMessageID string,
) (Message, error) {
	if err := contextError(ctx); err != nil {
		return Message{}, err
	}
	key, err := newExternalMessageKey(channel, externalMessageID)
	if err != nil {
		return Message{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return findMessageByExternalID(r.messages, r.messageIDByExternal, key)
}

// ListMessages returns a detached slice in append order.
func (r *MemoryRepository) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	conversationID, err := requiredString("conversation_id", conversationID)
	if err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.conversations[conversationID]; !exists {
		return nil, fmt.Errorf("%w: %q", ErrConversationNotFound, conversationID)
	}

	ids := r.messageIDsByConv[conversationID]
	messages := make([]Message, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, r.messages[id])
	}
	return messages, nil
}

// WithinTx serializes an in-memory transaction and applies its staged writes
// only when fn succeeds. The callback must use the supplied Transaction and
// must not perform HTTP, model, database, or other potentially blocking work.
func (r *MemoryRepository) WithinTx(ctx context.Context, fn func(Transaction) error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if fn == nil {
		return invalidArgument("transaction_callback", "is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.initializeLocked()

	tx := newMemoryTransaction(r)
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.commit()
	return nil
}

type stagedMessage struct {
	channel string
	message Message
}

type memoryTransaction struct {
	repository *MemoryRepository

	insertedConversations map[string]*Conversation
	updatedConversations  map[string]*Conversation
	insertedMessages      map[string]Message
	insertedMessageOrder  []stagedMessage
	insertedExternalIDs   map[externalMessageKey]string
}

func newMemoryTransaction(repository *MemoryRepository) *memoryTransaction {
	return &memoryTransaction{
		repository:            repository,
		insertedConversations: make(map[string]*Conversation),
		updatedConversations:  make(map[string]*Conversation),
		insertedMessages:      make(map[string]Message),
		insertedExternalIDs:   make(map[externalMessageKey]string),
	}
}

func (tx *memoryTransaction) FindConversation(id string) (*Conversation, error) {
	id, err := requiredString("id", id)
	if err != nil {
		return nil, err
	}
	if current, exists := tx.insertedConversations[id]; exists {
		return cloneConversation(current), nil
	}
	if current, exists := tx.updatedConversations[id]; exists {
		return cloneConversation(current), nil
	}
	return findConversation(tx.repository.conversations, id)
}

func (tx *memoryTransaction) InsertConversation(current *Conversation) error {
	if err := validateConversation(current); err != nil {
		return err
	}
	if _, exists := tx.insertedConversations[current.id]; exists {
		return fmt.Errorf("%w: %q", ErrConversationAlreadyExists, current.id)
	}
	if _, exists := tx.repository.conversations[current.id]; exists {
		return fmt.Errorf("%w: %q", ErrConversationAlreadyExists, current.id)
	}
	tx.insertedConversations[current.id] = cloneConversation(current)
	return nil
}

func (tx *memoryTransaction) UpdateConversation(current *Conversation) error {
	if err := validateConversation(current); err != nil {
		return err
	}
	if _, exists := tx.insertedConversations[current.id]; exists {
		tx.insertedConversations[current.id] = cloneConversation(current)
		return nil
	}
	if _, exists := tx.repository.conversations[current.id]; !exists {
		return fmt.Errorf("%w: %q", ErrConversationNotFound, current.id)
	}
	tx.updatedConversations[current.id] = cloneConversation(current)
	return nil
}

func (tx *memoryTransaction) FindMessageByExternalID(
	channel string,
	externalMessageID string,
) (Message, error) {
	key, err := newExternalMessageKey(channel, externalMessageID)
	if err != nil {
		return Message{}, err
	}
	if messageID, exists := tx.insertedExternalIDs[key]; exists {
		return tx.insertedMessages[messageID], nil
	}
	return findMessageByExternalID(
		tx.repository.messages,
		tx.repository.messageIDByExternal,
		key,
	)
}

func (tx *memoryTransaction) InsertMessage(channel string, message Message) error {
	channel, err := requiredString("channel", channel)
	if err != nil {
		return err
	}
	validated, err := validateMessage(message)
	if err != nil {
		return err
	}

	current, err := tx.FindConversation(validated.ConversationID())
	if err != nil {
		return err
	}
	if current.Channel() != channel {
		return invalidArgument("channel", "does not match the conversation channel")
	}
	if _, exists := tx.insertedMessages[validated.ID()]; exists {
		return fmt.Errorf("%w: %q", ErrMessageAlreadyExists, validated.ID())
	}
	if _, exists := tx.repository.messages[validated.ID()]; exists {
		return fmt.Errorf("%w: %q", ErrMessageAlreadyExists, validated.ID())
	}
	if validated.ExternalMessageID() != "" {
		key := externalMessageKey{channel: channel, id: validated.ExternalMessageID()}
		if _, exists := tx.insertedExternalIDs[key]; exists {
			return duplicateExternalMessageError(key)
		}
		if _, exists := tx.repository.messageIDByExternal[key]; exists {
			return duplicateExternalMessageError(key)
		}
		tx.insertedExternalIDs[key] = validated.ID()
	}

	tx.insertedMessages[validated.ID()] = validated
	tx.insertedMessageOrder = append(tx.insertedMessageOrder, stagedMessage{
		channel: channel,
		message: validated,
	})
	return nil
}

func (tx *memoryTransaction) commit() {
	for id, current := range tx.insertedConversations {
		tx.repository.conversations[id] = cloneConversation(current)
	}
	for id, current := range tx.updatedConversations {
		tx.repository.conversations[id] = cloneConversation(current)
	}
	for _, staged := range tx.insertedMessageOrder {
		message := staged.message
		tx.repository.messages[message.ID()] = message
		tx.repository.messageIDsByConv[message.ConversationID()] = append(
			tx.repository.messageIDsByConv[message.ConversationID()],
			message.ID(),
		)
		if message.ExternalMessageID() != "" {
			key := externalMessageKey{channel: staged.channel, id: message.ExternalMessageID()}
			tx.repository.messageIDByExternal[key] = message.ID()
		}
	}
}

func (r *MemoryRepository) initializeLocked() {
	if r.conversations == nil {
		r.conversations = make(map[string]*Conversation)
	}
	if r.messages == nil {
		r.messages = make(map[string]Message)
	}
	if r.messageIDsByConv == nil {
		r.messageIDsByConv = make(map[string][]string)
	}
	if r.messageIDByExternal == nil {
		r.messageIDByExternal = make(map[externalMessageKey]string)
	}
}

func findConversation(conversations map[string]*Conversation, id string) (*Conversation, error) {
	current, exists := conversations[id]
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrConversationNotFound, id)
	}
	return cloneConversation(current), nil
}

func findMessageByExternalID(
	messages map[string]Message,
	messageIDByExternal map[externalMessageKey]string,
	key externalMessageKey,
) (Message, error) {
	messageID, exists := messageIDByExternal[key]
	if !exists {
		return Message{}, fmt.Errorf(
			"%w: channel %q external ID %q",
			ErrMessageNotFound,
			key.channel,
			key.id,
		)
	}
	return messages[messageID], nil
}

func newExternalMessageKey(channel, externalMessageID string) (externalMessageKey, error) {
	channel, err := requiredString("channel", channel)
	if err != nil {
		return externalMessageKey{}, err
	}
	externalMessageID, err = requiredString("external_message_id", externalMessageID)
	if err != nil {
		return externalMessageKey{}, err
	}
	return externalMessageKey{channel: channel, id: externalMessageID}, nil
}

func duplicateExternalMessageError(key externalMessageKey) error {
	return fmt.Errorf(
		"%w: channel %q external ID %q",
		ErrDuplicateExternalMessage,
		key.channel,
		key.id,
	)
}

func validateMessage(message Message) (Message, error) {
	return NewMessage(MessageParams{
		ID:                message.ID(),
		ConversationID:    message.ConversationID(),
		ExternalMessageID: message.ExternalMessageID(),
		Sender:            message.Sender(),
		SenderID:          message.SenderID(),
		Content:           message.Content(),
		CreatedAt:         message.CreatedAt(),
	})
}

func cloneConversation(current *Conversation) *Conversation {
	if current == nil {
		return nil
	}
	cloned := *current
	return &cloned
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
	if current.status == StatusHumanActive && current.agentID == "" {
		return invalidArgument("agent_id", "is required while status is human_active")
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return invalidArgument("context", "is nil")
	}
	return ctx.Err()
}
