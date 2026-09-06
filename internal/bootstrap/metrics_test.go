package bootstrap_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func TestResolveMetricsConfigDisablesMetricsWithoutToken(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "missing token", values: map[string]string{}},
		{name: "empty token", values: map[string]string{"METRICS_AUTH_TOKEN": ""}},
		{name: "blank token", values: map[string]string{"METRICS_AUTH_TOKEN": " \t\n "}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := bootstrap.ResolveMetricsConfig(environment(test.values))
			if err != nil {
				t.Fatalf("ResolveMetricsConfig() error = %v", err)
			}
			if config.BearerToken != "" {
				t.Fatal("metrics must be disabled without a configured token")
			}
		})
	}
}

func TestResolveMetricsConfigAcceptsDedicatedStrongToken(t *testing.T) {
	token := strings.Repeat("m", bootstrap.MinimumMetricsAuthTokenLength)
	config, err := bootstrap.ResolveMetricsConfig(environment(map[string]string{
		"METRICS_AUTH_TOKEN": " \t" + token + "\n ",
		"API_AUTH_TOKEN":     strings.Repeat("a", bootstrap.MinimumAPIAuthTokenLength),
	}))
	if err != nil {
		t.Fatalf("ResolveMetricsConfig() error = %v", err)
	}
	if config.BearerToken != token {
		t.Fatal("metrics bearer token was not preserved after trimming outer whitespace")
	}
}

func TestResolveMetricsConfigRejectsInvalidConfigurationWithoutLeakingTokens(t *testing.T) {
	strongToken := strings.Repeat("m", bootstrap.MinimumMetricsAuthTokenLength)
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "nil environment lookup"},
		{
			name: "short token",
			values: map[string]string{
				"METRICS_AUTH_TOKEN": strings.Repeat("s", bootstrap.MinimumMetricsAuthTokenLength-1),
			},
		},
		{
			name: "token containing space",
			values: map[string]string{
				"METRICS_AUTH_TOKEN": strongToken + " " + strongToken,
			},
		},
		{
			name: "token containing tab",
			values: map[string]string{
				"METRICS_AUTH_TOKEN": strongToken + "\t" + strongToken,
			},
		},
		{
			name: "token containing unicode whitespace",
			values: map[string]string{
				"METRICS_AUTH_TOKEN": strongToken + "\u2003" + strongToken,
			},
		},
		{
			name: "same as API token",
			values: map[string]string{
				"METRICS_AUTH_TOKEN": strongToken,
				"API_AUTH_TOKEN":     strongToken,
			},
		},
		{
			name: "same as API token after trimming",
			values: map[string]string{
				"METRICS_AUTH_TOKEN": " " + strongToken + "\t",
				"API_AUTH_TOKEN":     "\n" + strongToken + " ",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var lookup bootstrap.LookupEnv
			if test.values != nil {
				lookup = environment(test.values)
			}
			config, err := bootstrap.ResolveMetricsConfig(lookup)
			if !errors.Is(err, bootstrap.ErrInvalidMetricsConfiguration) {
				t.Fatalf("ResolveMetricsConfig() error = %v, want %v", err, bootstrap.ErrInvalidMetricsConfiguration)
			}
			if config.BearerToken != "" {
				t.Fatal("invalid configuration must not return a bearer token")
			}
			for name, value := range test.values {
				if secret := strings.TrimSpace(value); secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatalf("configuration error leaked %s", name)
				}
			}
		})
	}
}
