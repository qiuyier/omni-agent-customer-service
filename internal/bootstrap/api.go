package bootstrap

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const (
	MinimumAPIAuthTokenLength = 32
	DefaultAPIRateLimitRPS    = 20.0
	DefaultAPIRateLimitBurst  = 40
)

var ErrInvalidAPIConfiguration = errors.New("bootstrap: invalid API configuration")

// APIConfig contains credentials used only at the external HTTP boundary.
// The application and domain layers never receive this token.
type APIConfig struct {
	BearerToken    string
	RateLimitRPS   float64
	RateLimitBurst int
}

// ResolveAPIConfig loads the shared API credential. Authentication is
// fail-closed: the HTTP server must not start with a missing or weak token.
func ResolveAPIConfig(lookup LookupEnv) (APIConfig, error) {
	if lookup == nil {
		return APIConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidAPIConfiguration,
		)
	}

	token := strings.TrimSpace(environmentValue(lookup, "API_AUTH_TOKEN"))
	if token == "" {
		return APIConfig{}, fmt.Errorf(
			"%w: API_AUTH_TOKEN must be set",
			ErrInvalidAPIConfiguration,
		)
	}
	if len(token) < MinimumAPIAuthTokenLength {
		return APIConfig{}, fmt.Errorf(
			"%w: API_AUTH_TOKEN must contain at least %d bytes",
			ErrInvalidAPIConfiguration,
			MinimumAPIAuthTokenLength,
		)
	}
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return APIConfig{}, fmt.Errorf(
			"%w: API_AUTH_TOKEN must not contain whitespace",
			ErrInvalidAPIConfiguration,
		)
	}

	rateLimitRPS, err := positiveFloatEnvironment(
		lookup,
		"API_RATE_LIMIT_RPS",
		DefaultAPIRateLimitRPS,
	)
	if err != nil {
		return APIConfig{}, err
	}
	rateLimitBurst, err := positiveIntEnvironment(
		lookup,
		"API_RATE_LIMIT_BURST",
		DefaultAPIRateLimitBurst,
	)
	if err != nil {
		return APIConfig{}, err
	}

	return APIConfig{
		BearerToken:    token,
		RateLimitRPS:   rateLimitRPS,
		RateLimitBurst: rateLimitBurst,
	}, nil
}

func positiveFloatEnvironment(
	lookup LookupEnv,
	name string,
	fallback float64,
) (float64, error) {
	value := strings.TrimSpace(environmentValue(lookup, name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf(
			"%w: %s must be a positive finite number",
			ErrInvalidAPIConfiguration,
			name,
		)
	}
	return parsed, nil
}

func positiveIntEnvironment(
	lookup LookupEnv,
	name string,
	fallback int,
) (int, error) {
	value := strings.TrimSpace(environmentValue(lookup, name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf(
			"%w: %s must be a positive integer",
			ErrInvalidAPIConfiguration,
			name,
		)
	}
	return parsed, nil
}
