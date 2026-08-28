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
	if config.RateLimitRPS != bootstrap.DefaultAPIRateLimitRPS ||
		config.RateLimitBurst != bootstrap.DefaultAPIRateLimitBurst {
		t.Fatalf("rate limit config = %#v", config)
	}
}

func TestResolveAPIConfigAcceptsRateLimitOverrides(t *testing.T) {
	config, err := bootstrap.ResolveAPIConfig(environment(map[string]string{
		"API_AUTH_TOKEN":       strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
		"API_RATE_LIMIT_RPS":   "12.5",
		"API_RATE_LIMIT_BURST": "25",
	}))
	if err != nil {
		t.Fatalf("ResolveAPIConfig() error = %v", err)
	}
	if config.RateLimitRPS != 12.5 || config.RateLimitBurst != 25 {
		t.Fatalf("rate limit config = %#v", config)
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
		{
			name: "zero requests per second",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":     strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_RPS": "0",
			}),
		},
		{
			name: "negative requests per second",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":     strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_RPS": "-1",
			}),
		},
		{
			name: "non-finite requests per second",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":     strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_RPS": "NaN",
			}),
		},
		{
			name: "infinite requests per second",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":     strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_RPS": "+Inf",
			}),
		},
		{
			name: "invalid requests per second",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":     strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_RPS": "fast",
			}),
		},
		{
			name: "zero burst",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":       strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_BURST": "0",
			}),
		},
		{
			name: "negative burst",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":       strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_BURST": "-1",
			}),
		},
		{
			name: "invalid burst",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":       strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_BURST": "1.5",
			}),
		},
		{
			name: "overflowing burst",
			lookup: environment(map[string]string{
				"API_AUTH_TOKEN":       strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
				"API_RATE_LIMIT_BURST": "999999999999999999999999999999999999",
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
