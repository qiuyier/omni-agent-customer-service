package bootstrap

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const MinimumMetricsAuthTokenLength = 32

var ErrInvalidMetricsConfiguration = errors.New("bootstrap: invalid metrics configuration")

// MetricsConfig contains the dedicated credential used at the metrics HTTP
// boundary. An empty BearerToken disables the metrics endpoint.
type MetricsConfig struct {
	BearerToken string
}

// ResolveMetricsConfig enables metrics only when METRICS_AUTH_TOKEN is set.
// A configured token must be strong and distinct from the API credential so a
// monitoring client cannot use its credential to invoke business endpoints.
func ResolveMetricsConfig(lookup LookupEnv) (MetricsConfig, error) {
	if lookup == nil {
		return MetricsConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidMetricsConfiguration,
		)
	}

	token := strings.TrimSpace(environmentValue(lookup, "METRICS_AUTH_TOKEN"))
	if token == "" {
		return MetricsConfig{}, nil
	}
	if len(token) < MinimumMetricsAuthTokenLength {
		return MetricsConfig{}, fmt.Errorf(
			"%w: METRICS_AUTH_TOKEN must contain at least %d bytes",
			ErrInvalidMetricsConfiguration,
			MinimumMetricsAuthTokenLength,
		)
	}
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return MetricsConfig{}, fmt.Errorf(
			"%w: METRICS_AUTH_TOKEN must not contain whitespace",
			ErrInvalidMetricsConfiguration,
		)
	}
	if token == strings.TrimSpace(environmentValue(lookup, "API_AUTH_TOKEN")) {
		return MetricsConfig{}, fmt.Errorf(
			"%w: METRICS_AUTH_TOKEN must differ from API_AUTH_TOKEN",
			ErrInvalidMetricsConfiguration,
		)
	}

	return MetricsConfig{BearerToken: token}, nil
}
