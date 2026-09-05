package bootstrap_test

import (
	"errors"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func TestResolveServerConfigUsesDefault(t *testing.T) {
	config, err := bootstrap.ResolveServerConfig(environment(nil))
	if err != nil {
		t.Fatalf("ResolveServerConfig() error = %v", err)
	}
	if config.ShutdownTimeout != bootstrap.DefaultHTTPShutdownTimeout {
		t.Fatalf(
			"shutdown timeout = %v, want %v",
			config.ShutdownTimeout,
			bootstrap.DefaultHTTPShutdownTimeout,
		)
	}
}

func TestResolveServerConfigAcceptsShutdownTimeout(t *testing.T) {
	config, err := bootstrap.ResolveServerConfig(environment(map[string]string{
		"SERVER_SHUTDOWN_TIMEOUT": " 2m15s ",
	}))
	if err != nil {
		t.Fatalf("ResolveServerConfig() error = %v", err)
	}
	if config.ShutdownTimeout != 2*time.Minute+15*time.Second {
		t.Fatalf("shutdown timeout = %v, want 2m15s", config.ShutdownTimeout)
	}
}

func TestResolveServerConfigRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		lookup bootstrap.LookupEnv
	}{
		{name: "nil environment lookup"},
		{name: "zero timeout", lookup: environment(map[string]string{"SERVER_SHUTDOWN_TIMEOUT": "0s"})},
		{name: "negative timeout", lookup: environment(map[string]string{"SERVER_SHUTDOWN_TIMEOUT": "-1s"})},
		{name: "invalid timeout", lookup: environment(map[string]string{"SERVER_SHUTDOWN_TIMEOUT": "soon"})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bootstrap.ResolveServerConfig(test.lookup)
			if !errors.Is(err, bootstrap.ErrInvalidServerConfiguration) {
				t.Fatalf(
					"ResolveServerConfig() error = %v, want %v",
					err,
					bootstrap.ErrInvalidServerConfiguration,
				)
			}
		})
	}
}
