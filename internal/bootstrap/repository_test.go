package bootstrap_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func TestResolveRepositoryConfigDefaultsToMemory(t *testing.T) {
	config, err := bootstrap.ResolveRepositoryConfig(environment(nil))
	if err != nil {
		t.Fatalf("ResolveRepositoryConfig() error = %v", err)
	}
	if config.Driver != bootstrap.StorageDriverMemory || config.DatabaseURL != "" {
		t.Fatalf("config = %#v", config)
	}
}

func TestResolveRepositoryConfigSelectsPostgres(t *testing.T) {
	config, err := bootstrap.ResolveRepositoryConfig(environment(map[string]string{
		"STORAGE_DRIVER": " PostgreSQL ",
		"DATABASE_URL":   " postgres://user:secret@database/service ",
	}))
	if err != nil {
		t.Fatalf("ResolveRepositoryConfig() error = %v", err)
	}
	if config.Driver != bootstrap.StorageDriverPostgres ||
		config.DatabaseURL != "postgres://user:secret@database/service" {
		t.Fatalf("config = %#v", config)
	}
}

func TestResolveRepositoryConfigRejectsInvalidSettingsWithoutLeakingURL(t *testing.T) {
	tests := []struct {
		name   string
		lookup bootstrap.LookupEnv
	}{
		{name: "nil lookup"},
		{name: "unknown driver", lookup: environment(map[string]string{
			"STORAGE_DRIVER": "redis",
			"DATABASE_URL":   "postgres://user:secret-that-must-not-leak@database/service",
		})},
		{name: "postgres without URL", lookup: environment(map[string]string{
			"STORAGE_DRIVER": "postgres",
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bootstrap.ResolveRepositoryConfig(test.lookup)
			if !errors.Is(err, bootstrap.ErrInvalidStorageConfiguration) {
				t.Fatalf("error = %v, want ErrInvalidStorageConfiguration", err)
			}
			if strings.Contains(err.Error(), "secret-that-must-not-leak") {
				t.Fatalf("error leaked database URL: %v", err)
			}
		})
	}
}

func TestNewConversationRepositoryCreatesMemoryRuntime(t *testing.T) {
	runtime, err := bootstrap.NewConversationRepository(context.Background(), bootstrap.RepositoryConfig{
		Driver: bootstrap.StorageDriverMemory,
	})
	if err != nil {
		t.Fatalf("NewConversationRepository() error = %v", err)
	}
	if runtime.Repository == nil {
		t.Fatal("Repository = nil")
	}
	if _, ok := runtime.Repository.(*conversation.MemoryRepository); !ok {
		t.Fatalf("Repository type = %T", runtime.Repository)
	}
	runtime.Close()
	runtime.Close()
}

func TestNewConversationRepositoryHonorsContextAndDriver(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootstrap.NewConversationRepository(cancelled, bootstrap.RepositoryConfig{
		Driver: bootstrap.StorageDriverMemory,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v, want context.Canceled", err)
	}
	if _, err := bootstrap.NewConversationRepository(context.Background(), bootstrap.RepositoryConfig{
		Driver: "unknown",
	}); !errors.Is(err, bootstrap.ErrInvalidStorageConfiguration) {
		t.Fatalf("unknown driver error = %v", err)
	}
}
