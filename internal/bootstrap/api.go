package bootstrap

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const MinimumAPIAuthTokenLength = 32

var ErrInvalidAPIConfiguration = errors.New("bootstrap: invalid API configuration")

// APIConfig contains credentials used only at the external HTTP boundary.
// The application and domain layers never receive this token.
type APIConfig struct {
	BearerToken string
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

	return APIConfig{BearerToken: token}, nil
}
