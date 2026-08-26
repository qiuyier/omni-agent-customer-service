package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	postgresrepository "github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
)

// StorageDriver selects the concrete adapter behind conversation.Repository.
type StorageDriver string

const (
	StorageDriverMemory   StorageDriver = "memory"
	StorageDriverPostgres StorageDriver = "postgres"
)

var ErrInvalidStorageConfiguration = errors.New("bootstrap: invalid storage configuration")

// RepositoryConfig contains persistence settings resolved at the composition
// boundary. DatabaseURL is never passed into the application or domain layer.
type RepositoryConfig struct {
	Driver      StorageDriver
	DatabaseURL string
}

// RepositoryRuntime couples the persistence port with adapter lifecycle. The
// Service receives only Repository; cmd/server retains Runtime and closes it.
type RepositoryRuntime struct {
	Repository conversation.Repository

	closeOnce sync.Once
	close     func()
}

// Close releases resources owned by the concrete adapter. It is safe to call
// repeatedly; memory storage has no resources and treats Close as a no-op.
func (r *RepositoryRuntime) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		if r.close != nil {
			r.close()
		}
	})
}

// ResolveRepositoryConfig reads storage selection from the process boundary.
// Memory remains the default for zero-setup local development; PostgreSQL must
// be selected explicitly and always requires DATABASE_URL.
func ResolveRepositoryConfig(lookup LookupEnv) (RepositoryConfig, error) {
	if lookup == nil {
		return RepositoryConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidStorageConfiguration,
		)
	}

	driverName := strings.ToLower(firstNonBlank(
		environmentValue(lookup, "STORAGE_DRIVER"),
		string(StorageDriverMemory),
	))
	config := RepositoryConfig{}
	switch driverName {
	case string(StorageDriverMemory):
		config.Driver = StorageDriverMemory
		return config, nil
	case string(StorageDriverPostgres), "postgresql":
		config.Driver = StorageDriverPostgres
		config.DatabaseURL = firstNonBlank(environmentValue(lookup, "DATABASE_URL"))
		if config.DatabaseURL == "" {
			return RepositoryConfig{}, fmt.Errorf(
				"%w: DATABASE_URL must be set when STORAGE_DRIVER=postgres",
				ErrInvalidStorageConfiguration,
			)
		}
		return config, nil
	default:
		return RepositoryConfig{}, fmt.Errorf(
			"%w: STORAGE_DRIVER %q is not supported",
			ErrInvalidStorageConfiguration,
			driverName,
		)
	}
}

// NewConversationRepository builds the selected persistence adapter while
// preserving the application-facing conversation.Repository boundary.
func NewConversationRepository(
	ctx context.Context,
	config RepositoryConfig,
) (*RepositoryRuntime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidStorageConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch config.Driver {
	case StorageDriverMemory:
		return &RepositoryRuntime{Repository: conversation.NewMemoryRepository()}, nil
	case StorageDriverPostgres:
		repository, err := postgresrepository.Open(ctx, postgresrepository.Config{
			DatabaseURL: config.DatabaseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("create PostgreSQL repository: %w", err)
		}
		return &RepositoryRuntime{
			Repository: repository,
			close:      repository.Close,
		}, nil
	default:
		return nil, fmt.Errorf(
			"%w: storage driver %q is not supported",
			ErrInvalidStorageConfiguration,
			config.Driver,
		)
	}
}
