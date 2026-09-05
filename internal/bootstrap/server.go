package bootstrap

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultHTTPShutdownTimeout is longer than DefaultAIReplyTimeout so an
	// admitted model request normally has time to finish and persist its reply.
	DefaultHTTPShutdownTimeout = 90 * time.Second
)

var ErrInvalidServerConfiguration = errors.New("bootstrap: invalid server configuration")

// ServerConfig contains process-level HTTP lifecycle policy. It is kept out
// of APIConfig because shutdown timing is operational policy, not API
// authentication or request-limiting policy.
type ServerConfig struct {
	ShutdownTimeout time.Duration
}

// ResolveServerConfig loads and validates the bounded graceful-shutdown time.
func ResolveServerConfig(lookup LookupEnv) (ServerConfig, error) {
	if lookup == nil {
		return ServerConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidServerConfiguration,
		)
	}

	value := strings.TrimSpace(environmentValue(lookup, "SERVER_SHUTDOWN_TIMEOUT"))
	if value == "" {
		return ServerConfig{ShutdownTimeout: DefaultHTTPShutdownTimeout}, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return ServerConfig{}, fmt.Errorf(
			"%w: SERVER_SHUTDOWN_TIMEOUT must be a positive Go duration",
			ErrInvalidServerConfiguration,
		)
	}
	return ServerConfig{ShutdownTimeout: timeout}, nil
}
