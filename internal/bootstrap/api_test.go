package bootstrap_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func TestResolveAPIConfigAcceptsStrongToken(t *testing.T) {
	token := strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength)
	config, err := bootstrap.ResolveAPIConfig(environment(map[string]string{
		"API_AUTH_TOKEN": "  " + token + "  ",
	}))
	if err != nil {
		t.Fatalf("ResolveAPIConfig() error = %v", err)
	}
	if config.BearerToken != token {
		t.Fatalf("bearer token length = %d, want %d", len(config.BearerToken), len(token))
	}
}

func TestResolveAPIConfigRejectsInvalidConfigurationWithoutLeakingToken(t *testing.T) {
	secret := "short-secret-that-must-not-leak"
	tests := []struct {
		name   string
		lookup bootstrap.LookupEnv
	}{
		{name: "nil environment lookup"},
		{name: "missing token", lookup: environment(map[string]string{})},
		{name: "blank token", lookup: environment(map[string]string{"API_AUTH_TOKEN": "   "})},
		{name: "short token", lookup: environment(map[string]string{"API_AUTH_TOKEN": secret})},
		{
			name: "token containing whitespace",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN": strings.Repeat("a", 20) + " " + strings.Repeat("b", 20),
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bootstrap.ResolveAPIConfig(test.lookup)
			if !errors.Is(err, bootstrap.ErrInvalidAPIConfiguration) {
				t.Fatalf(
					"ResolveAPIConfig() error = %v, want %v",
					err,
					bootstrap.ErrInvalidAPIConfiguration,
				)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("configuration error leaked token: %v", err)
			}
		})
	}
}
