// Package postgres implements the conversation persistence port with pgx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres/schema"
)

var (
	// ErrInvalidConfiguration indicates that the PostgreSQL adapter cannot be
	// constructed from the supplied settings.
	ErrInvalidConfiguration = errors.New("postgres: invalid configuration")

	// ErrSchemaNotReady indicates that migrations have not brought the database
	// to the exact version required by this application build.
	ErrSchemaNotReady = errors.New("postgres: schema not ready")
)

// Config contains the settings required to open a PostgreSQL repository.
// Pool tuning can be supplied through pgx-compatible DATABASE_URL parameters.
type Config struct {
	DatabaseURL string
}

// Repository persists conversations and messages in PostgreSQL. Its lifecycle
// belongs to the composition root, not to the customer-service domain.
type Repository struct {
	pool     *pgxpool.Pool
	ownsPool bool
}

// Open creates a connection pool and verifies that PostgreSQL is reachable.
// The URL itself is deliberately never included in returned errors.
func Open(ctx context.Context, config Config) (*Repository, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	databaseURL := strings.TrimSpace(config.DatabaseURL)
	if databaseURL == "" {
		return nil, fmt.Errorf("%w: database URL is required", ErrInvalidConfiguration)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: database URL is invalid", ErrInvalidConfiguration)
	}
	if poolConfig.ConnConfig.RuntimeParams["application_name"] == "" {
		poolConfig.ConnConfig.RuntimeParams["application_name"] = "omni-agent-customer-service"
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("postgres: create connection pool: %w", err)
	}
	repository := &Repository{pool: pool, ownsPool: true}
	if err := repository.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := repository.CheckSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return repository, nil
}

// New creates a repository around an existing, caller-owned pool. Close does
// not close a borrowed pool; its original owner remains responsible for it.
func New(pool *pgxpool.Pool) (*Repository, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: connection pool is nil", ErrInvalidConfiguration)
	}
	return &Repository{pool: pool}, nil
}

// Ping verifies that the repository can acquire a working database connection.
func (r *Repository) Ping(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := r.validate(); err != nil {
		return err
	}
	if err := r.pool.Ping(ctx); err != nil {
		return databaseError(ctx, "ping database", err)
	}
	return nil
}

// CheckSchema verifies both the Tern ledger and the required business tables.
// It prevents a reachable but unmigrated database from passing server startup.
func (r *Repository) CheckSchema(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := r.validate(); err != nil {
		return err
	}

	var (
		version              int32
		conversationsPresent bool
		messagesPresent      bool
		receiptsPresent      bool
		knowledgePresent     bool
	)
	err := r.pool.QueryRow(ctx, `
SELECT
    version,
    to_regclass('customer_service.conversations') IS NOT NULL,
    to_regclass('customer_service.messages') IS NOT NULL,
    to_regclass('customer_service.desk_read_receipts') IS NOT NULL,
    to_regclass('customer_service.knowledge_management') IS NOT NULL
FROM `+schema.VersionTable+`
`).Scan(&version, &conversationsPresent, &messagesPresent, &receiptsPresent, &knowledgePresent)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("%w: run cmd/migrate before starting the server", ErrSchemaNotReady)
	}
	if version != schema.CurrentVersion {
		return fmt.Errorf(
			"%w: database version is %d, application requires %d; run cmd/migrate",
			ErrSchemaNotReady,
			version,
			schema.CurrentVersion,
		)
	}
	if !conversationsPresent || !messagesPresent || !receiptsPresent || !knowledgePresent {
		return fmt.Errorf(
			"%w: required business tables are missing; run cmd/migrate",
			ErrSchemaNotReady,
		)
	}
	return nil
}

// Close releases every connection owned by the pool. Open callers should call
// it once during process shutdown.
func (r *Repository) Close() {
	if r != nil && r.pool != nil && r.ownsPool {
		r.pool.Close()
	}
}

// FindConversation returns one detached, domain-validated aggregate.
func (r *Repository) FindConversation(
	ctx context.Context,
	id string,
) (*conversation.Conversation, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	id, err := requiredString("id", id)
	if err != nil {
		return nil, err
	}
	return queryConversation(ctx, r.pool, findConversationSQL, id)
}

// FindMessageByExternalID looks up a channel-scoped idempotency key.
func (r *Repository) FindMessageByExternalID(
	ctx context.Context,
	channel string,
	externalMessageID string,
) (conversation.Message, error) {
	if err := contextError(ctx); err != nil {
		return conversation.Message{}, err
	}
	if err := r.validate(); err != nil {
		return conversation.Message{}, err
	}
	channel, err := requiredString("channel", channel)
	if err != nil {
		return conversation.Message{}, err
	}
	externalMessageID, err = requiredString("external_message_id", externalMessageID)
	if err != nil {
		return conversation.Message{}, err
	}
	return queryMessage(
		ctx,
		r.pool,
		findMessageByExternalIDSQL,
		conversation.ErrMessageNotFound,
		"find message by external ID",
		channel,
		externalMessageID,
	)
}

// FindReplyByTriggerMessageID returns the unique reply linked to a trigger.
func (r *Repository) FindReplyByTriggerMessageID(
	ctx context.Context,
	triggerMessageID string,
) (conversation.Message, error) {
	if err := contextError(ctx); err != nil {
		return conversation.Message{}, err
	}
	if err := r.validate(); err != nil {
		return conversation.Message{}, err
	}
	triggerMessageID, err := requiredString("trigger_message_id", triggerMessageID)
	if err != nil {
		return conversation.Message{}, err
	}
	return queryMessage(
		ctx,
		r.pool,
		findReplyByTriggerMessageIDSQL,
		conversation.ErrReplyNotFound,
		"find reply by trigger message ID",
		triggerMessageID,
	)
}

// ListMessages returns messages in their database-assigned append order.
func (r *Repository) ListMessages(
	ctx context.Context,
	conversationID string,
) ([]conversation.Message, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	conversationID, err := requiredString("conversation_id", conversationID)
	if err != nil {
		return nil, err
	}
	if _, err := queryConversation(ctx, r.pool, findConversationSQL, conversationID); err != nil {
		return nil, err
	}
	return queryMessages(ctx, r.pool, conversationID)
}

func (r *Repository) validate() error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("%w: repository is not initialized", ErrInvalidConfiguration)
	}
	return nil
}
