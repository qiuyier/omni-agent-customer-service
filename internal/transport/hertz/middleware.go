package hertz

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/qiuyier/omni-agent-kit/fault"
)

const (
	RequestIDHeader               = "X-Request-ID"
	maximumRequestIDLength        = 128
	bearerAuthenticationChallenge = `Bearer realm="omni-agent-customer-service"`
)

type requestIDContextKey struct{}

// SecurityMiddleware owns HTTP-only request identity and authentication. It
// stores a digest rather than the plaintext configured bearer token.
type SecurityMiddleware struct {
	bearerTokenDigest [sha256.Size]byte
}

// NewSecurityMiddleware builds the fail-closed middleware used by /v1 routes.
func NewSecurityMiddleware(bearerToken string) (*SecurityMiddleware, error) {
	token := strings.TrimSpace(bearerToken)
	if token == "" {
		return nil, invalidDependency("API bearer token is blank")
	}
	return &SecurityMiddleware{
		bearerTokenDigest: sha256.Sum256([]byte(token)),
	}, nil
}

// RequestID accepts a safe caller-supplied identifier or creates a new UUID.
// The value is returned in the response and propagated through context.Context.
func (m *SecurityMiddleware) RequestID(
	ctx context.Context,
	requestContext *app.RequestContext,
) {
	requestIDHeaders := requestContext.Request.Header.PeekAll(RequestIDHeader)
	requestID := ""
	if len(requestIDHeaders) == 1 {
		requestID = strings.TrimSpace(string(requestIDHeaders[0]))
	}
	if !validRequestID(requestID) {
		requestID = uuid.NewString()
	}

	requestContext.Response.Header.Set(RequestIDHeader, requestID)
	requestContext.Next(context.WithValue(ctx, requestIDContextKey{}, requestID))
}

// RequireBearerToken protects a route without exposing why authentication
// failed. The health endpoint does not install this middleware.
func (m *SecurityMiddleware) RequireBearerToken(
	ctx context.Context,
	requestContext *app.RequestContext,
) {
	authorizationHeaders := requestContext.Request.Header.PeekAll(consts.HeaderAuthorization)
	token, ok := "", false
	if len(authorizationHeaders) == 1 {
		token, ok = parseBearerToken(authorizationHeaders[0])
	}
	if !ok || !m.authenticate(token) {
		requestContext.Response.Header.Set(
			consts.HeaderWWWAuthenticate,
			bearerAuthenticationChallenge,
		)
		writeProblem(requestContext, fault.New(fault.CodeUnauthenticated, ""))
		return
	}

	requestContext.Next(ctx)
}

// RequestIDFromContext returns the identifier installed by RequestID.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	requestID, ok := ctx.Value(requestIDContextKey{}).(string)
	return requestID, ok && requestID != ""
}

func (m *SecurityMiddleware) authenticate(token string) bool {
	if m == nil {
		return false
	}
	candidateDigest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(
		candidateDigest[:],
		m.bearerTokenDigest[:],
	) == 1
}

func parseBearerToken(header []byte) (string, bool) {
	parts := strings.Fields(string(header))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], parts[1] != ""
}

func validRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > maximumRequestIDLength {
		return false
	}
	for index := 0; index < len(requestID); index++ {
		character := requestID[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' || character == ':' {
			continue
		}
		return false
	}
	return true
}
