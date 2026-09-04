package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strings"

	hertzzap "github.com/hertz-contrib/logger/zap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	DefaultLogLevel  = "info"
	DefaultLogFormat = "json"
	serviceLogName   = "omni-agent-customer-service"
)

var ErrInvalidLoggingConfiguration = errors.New("bootstrap: invalid logging configuration")

// LoggingConfig contains only operational logging policy. It never contains
// request data, model input, or credentials.
type LoggingConfig struct {
	Level  string
	Format string
}

// ResolveLoggingConfig reads and validates the process-wide Zap settings.
func ResolveLoggingConfig(lookup LookupEnv) (LoggingConfig, error) {
	if lookup == nil {
		return LoggingConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidLoggingConfiguration,
		)
	}
	config := LoggingConfig{
		Level: strings.ToLower(firstNonBlank(
			environmentValue(lookup, "LOG_LEVEL"),
			DefaultLogLevel,
		)),
		Format: strings.ToLower(firstNonBlank(
			environmentValue(lookup, "LOG_FORMAT"),
			DefaultLogFormat,
		)),
	}
	if _, err := zapLevel(config.Level); err != nil {
		return LoggingConfig{}, err
	}
	if config.Format != "json" && config.Format != "console" {
		return LoggingConfig{}, fmt.Errorf(
			"%w: LOG_FORMAT must be json or console",
			ErrInvalidLoggingConfiguration,
		)
	}
	return config, nil
}

// NewZapLogger creates the shared Zap-backed logger used by application logs
// and Hertz's internal hlog facade.
func NewZapLogger(config LoggingConfig) (*hertzzap.Logger, error) {
	return newZapLogger(config, zapcore.Lock(os.Stdout))
}

func newZapLogger(
	config LoggingConfig,
	output zapcore.WriteSyncer,
) (*hertzzap.Logger, error) {
	if output == nil {
		return nil, fmt.Errorf(
			"%w: log output is nil",
			ErrInvalidLoggingConfiguration,
		)
	}
	level, err := zapLevel(strings.ToLower(strings.TrimSpace(config.Level)))
	if err != nil {
		return nil, err
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.MessageKey = "message"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeDuration = zapcore.MillisDurationEncoder

	var encoder zapcore.Encoder
	switch strings.ToLower(strings.TrimSpace(config.Format)) {
	case "json":
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	case "console":
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	default:
		return nil, fmt.Errorf(
			"%w: LOG_FORMAT must be json or console",
			ErrInvalidLoggingConfiguration,
		)
	}

	return hertzzap.NewLogger(
		hertzzap.WithCoreEnc(encoder),
		hertzzap.WithCoreWs(output),
		hertzzap.WithCoreLevel(zap.NewAtomicLevelAt(level)),
		hertzzap.WithZapOptions(
			zap.Fields(zap.String("service", serviceLogName)),
			zap.ErrorOutput(zapcore.Lock(os.Stderr)),
		),
	), nil
}

func zapLevel(value string) (zapcore.Level, error) {
	switch value {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf(
			"%w: LOG_LEVEL must be debug, info, warn, or error",
			ErrInvalidLoggingConfiguration,
		)
	}
}
