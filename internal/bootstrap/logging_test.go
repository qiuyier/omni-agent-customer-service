package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func TestResolveLoggingConfigUsesDefaultsAndNormalizesOverrides(t *testing.T) {
	defaultConfig, err := bootstrap.ResolveLoggingConfig(loggingEnvironment(nil))
	if err != nil {
		t.Fatalf("ResolveLoggingConfig() error = %v", err)
	}
	if defaultConfig.Level != bootstrap.DefaultLogLevel ||
		defaultConfig.Format != bootstrap.DefaultLogFormat {
		t.Fatalf("default logging config = %#v", defaultConfig)
	}

	overridden, err := bootstrap.ResolveLoggingConfig(loggingEnvironment(map[string]string{
		"LOG_LEVEL":  "  DEBUG  ",
		"LOG_FORMAT": "  CONSOLE  ",
	}))
	if err != nil {
		t.Fatalf("ResolveLoggingConfig() error = %v", err)
	}
	if overridden.Level != "debug" || overridden.Format != "console" {
		t.Fatalf("overridden logging config = %#v", overridden)
	}
}

func TestResolveLoggingConfigRejectsInvalidValuesWithoutEchoingThem(t *testing.T) {
	sensitiveMarker := "sk-test-api-key-must-not-leak"
	tests := []struct {
		name   string
		lookup bootstrap.LookupEnv
	}{
		{name: "nil environment lookup"},
		{
			name: "invalid level",
			lookup: loggingEnvironment(map[string]string{
				"LOG_LEVEL": sensitiveMarker,
			}),
		},
		{
			name: "invalid format",
			lookup: loggingEnvironment(map[string]string{
				"LOG_FORMAT": sensitiveMarker,
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := bootstrap.ResolveLoggingConfig(test.lookup)
			if !errors.Is(err, bootstrap.ErrInvalidLoggingConfiguration) {
				t.Fatalf(
					"ResolveLoggingConfig() error = %v, want %v",
					err,
					bootstrap.ErrInvalidLoggingConfiguration,
				)
			}
			if strings.Contains(err.Error(), sensitiveMarker) {
				t.Fatalf("logging configuration error leaked input: %v", err)
			}
		})
	}
}

func TestNewZapLoggerRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config bootstrap.LoggingConfig
	}{
		{
			name: "invalid level",
			config: bootstrap.LoggingConfig{
				Level:  "trace",
				Format: "json",
			},
		},
		{
			name: "invalid format",
			config: bootstrap.LoggingConfig{
				Level:  "info",
				Format: "text",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger, err := bootstrap.NewZapLogger(test.config)
			if !errors.Is(err, bootstrap.ErrInvalidLoggingConfiguration) {
				t.Fatalf(
					"NewZapLogger() error = %v, want %v",
					err,
					bootstrap.ErrInvalidLoggingConfiguration,
				)
			}
			if logger != nil {
				t.Fatal("NewZapLogger() returned a logger for invalid config")
			}
		})
	}
}

func TestNewZapLoggerSupportsJSONAndConsoleEncoding(t *testing.T) {
	for _, format := range []string{"json", "console"} {
		t.Run(format, func(t *testing.T) {
			logger, err := bootstrap.NewZapLogger(bootstrap.LoggingConfig{
				Level:  "info",
				Format: format,
			})
			if err != nil {
				t.Fatalf("NewZapLogger() error = %v", err)
			}

			var output bytes.Buffer
			logger.SetOutput(&output)
			logger.Logger().Debug("filtered-debug-message")
			logger.Logger().Info(
				"visible-log-message",
				zap.String("component", "logging-test"),
			)
			logger.Sync()

			logged := output.String()
			if strings.Contains(logged, "filtered-debug-message") {
				t.Fatalf("info logger emitted debug event: %s", logged)
			}
			for _, want := range []string{
				"visible-log-message",
				"omni-agent-customer-service",
				"logging-test",
			} {
				if !strings.Contains(logged, want) {
					t.Fatalf("encoded log %q does not contain %q", logged, want)
				}
			}

			if format != "json" {
				return
			}
			var record map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
				t.Fatalf("JSON log is invalid: %v; output = %q", err, logged)
			}
			for field, want := range map[string]any{
				"level":     "info",
				"message":   "visible-log-message",
				"service":   "omni-agent-customer-service",
				"component": "logging-test",
			} {
				if got := record[field]; got != want {
					t.Errorf("JSON field %q = %#v, want %#v", field, got, want)
				}
			}
			if _, ok := record["timestamp"]; !ok {
				t.Error("JSON log does not contain timestamp")
			}
		})
	}
}

func loggingEnvironment(values map[string]string) bootstrap.LookupEnv {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}
