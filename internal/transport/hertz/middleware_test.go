package hertz

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-kit/fault"
)

const testAPIAuthToken = "customer-service-test-token-0123456789"

func TestNewSecurityMiddlewareRejectsBlankToken(t *testing.T) {
	middleware, err := NewSecurityMiddleware("   ")
	if !errors.Is(err, errInvalidDependency) {
		t.Fatalf("NewSecurityMiddleware() error = %v, want %v", err, errInvalidDependency)
	}
	if middleware != nil {
		t.Fatalf("NewSecurityMiddleware() = %#v, want nil", middleware)
	}
}

func TestRequestIDPreservesSafeValueAndPropagatesContext(t *testing.T) {
	middleware := newTestSecurityMiddleware(t)
	httpServer := server.New()
	httpServer.Engine.Use(middleware.RequestID)
	httpServer.Engine.GET("/inspect", func(ctx context.Context, requestContext *app.RequestContext) {
		requestID, ok := RequestIDFromContext(ctx)
		if !ok {
			requestContext.String(http.StatusInternalServerError, "missing request ID")
			return
		}
		requestContext.String(http.StatusOK, requestID)
	})

	const requestID = "client.request-123"
	response := ut.PerformRequest(
		httpServer.Engine,
		http.MethodGet,
		"/inspect",
		nil,
		ut.Header{Key: RequestIDHeader, Value: requestID},
	).Result()

	assertStatus(t, response, http.StatusOK)
	if got := string(response.Header.Peek(RequestIDHeader)); got != requestID {
		t.Fatalf("response request ID = %q, want %q", got, requestID)
	}
	if got := string(response.Body()); got != requestID {
		t.Fatalf("context request ID = %q, want %q", got, requestID)
	}
}

func TestRequestIDReplacesMissingOrUnsafeValues(t *testing.T) {
	tests := []struct {
		name    string
		headers []ut.Header
	}{
		{name: "missing"},
		{name: "unsafe character", headers: []ut.Header{{Key: RequestIDHeader, Value: "bad/id"}}},
		{
			name: "too long",
			headers: []ut.Header{{
				Key: RequestIDHeader, Value: strings.Repeat("a", maximumRequestIDLength+1),
			}},
		},
		{
			name: "duplicate",
			headers: []ut.Header{
				{Key: RequestIDHeader, Value: "request-1"},
				{Key: RequestIDHeader, Value: "request-2"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			middleware := newTestSecurityMiddleware(t)
			httpServer := server.New()
			httpServer.Engine.Use(middleware.RequestID)
			httpServer.Engine.GET("/health", func(_ context.Context, ctx *app.RequestContext) {
				ctx.Status(http.StatusNoContent)
			})

			response := ut.PerformRequest(
				httpServer.Engine,
				http.MethodGet,
				"/health",
				nil,
				test.headers...,
			).Result()
			assertStatus(t, response, http.StatusNoContent)
			requestID := string(response.Header.Peek(RequestIDHeader))
			if _, err := uuid.Parse(requestID); err != nil {
				t.Fatalf("generated request ID = %q: %v", requestID, err)
			}
		})
	}
}

func TestProtectedRoutesRequireBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		target string
		body   *ut.Body
	}{
		{
			name:   "receive message",
			target: "/v1/conversations/conversation-1/messages",
			body: &ut.Body{
				Body: strings.NewReader(validReceiveMessageJSON),
				Len:  len(validReceiveMessageJSON),
			},
		},
		{
			name:   "generate reply",
			target: "/v1/conversations/conversation-1/messages/message-1/reply",
		},
		{
			name:   "stream reply",
			target: "/v1/conversations/conversation-1/messages/message-1/reply/stream",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application := &receiverStub{}
			engine := newTestEngine(t, application)
			response := ut.PerformRequest(
				engine,
				http.MethodPost,
				test.target,
				test.body,
			).Result()

			assertAuthenticationProblem(t, response)
			if application.calls != 0 || application.replyCalls != 0 || application.streamCalls != 0 {
				t.Fatalf("application calls = receive:%d reply:%d stream:%d, want all zero",
					application.calls, application.replyCalls, application.streamCalls)
			}
		})
	}
}

func TestBearerAuthenticationRejectsMalformedOrWrongCredentials(t *testing.T) {
	tests := []struct {
		name    string
		headers []ut.Header
	}{
		{name: "missing"},
		{name: "basic scheme", headers: []ut.Header{{Key: consts.HeaderAuthorization, Value: "Basic abc"}}},
		{name: "missing credential", headers: []ut.Header{{Key: consts.HeaderAuthorization, Value: "Bearer"}}},
		{name: "wrong token", headers: []ut.Header{{Key: consts.HeaderAuthorization, Value: "Bearer wrong-token"}}},
		{name: "token is case sensitive", headers: []ut.Header{{Key: consts.HeaderAuthorization, Value: "Bearer " + strings.ToUpper(testAPIAuthToken)}}},
		{name: "extra field", headers: []ut.Header{{Key: consts.HeaderAuthorization, Value: "Bearer " + testAPIAuthToken + " extra"}}},
		{
			name: "duplicate",
			headers: []ut.Header{
				{Key: consts.HeaderAuthorization, Value: "Bearer " + testAPIAuthToken},
				{Key: consts.HeaderAuthorization, Value: "Bearer " + testAPIAuthToken},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application := &receiverStub{}
			engine := newTestEngine(t, application)
			headers := append([]ut.Header{
				{Key: "Content-Type", Value: "application/json; charset=utf-8"},
				{Key: RequestIDHeader, Value: "auth-test-request"},
			}, test.headers...)
			response := ut.PerformRequest(
				engine,
				http.MethodPost,
				receiveMessagePath,
				&ut.Body{Body: strings.NewReader(validReceiveMessageJSON), Len: len(validReceiveMessageJSON)},
				headers...,
			).Result()

			assertAuthenticationProblem(t, response)
			if got := string(response.Header.Peek(RequestIDHeader)); got != "auth-test-request" {
				t.Fatalf("request ID = %q, want %q", got, "auth-test-request")
			}
			if application.calls != 0 {
				t.Fatalf("application calls = %d, want 0", application.calls)
			}
			if strings.Contains(string(response.Body()), testAPIAuthToken) {
				t.Fatalf("authentication response leaked token: %s", response.Body())
			}
		})
	}
}

func TestBearerAuthenticationAcceptsCaseInsensitiveScheme(t *testing.T) {
	application := &receiverStub{result: customerservice.ReceiveMessageResult{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		Status:         conversation.StatusBotActive,
		NextAction:     customerservice.NextActionGenerateBotReply,
	}}
	engine := newTestEngine(t, application)
	response := ut.PerformRequest(
		engine,
		http.MethodPost,
		receiveMessagePath,
		&ut.Body{Body: strings.NewReader(validReceiveMessageJSON), Len: len(validReceiveMessageJSON)},
		ut.Header{Key: "Content-Type", Value: "application/json; charset=utf-8"},
		ut.Header{Key: consts.HeaderAuthorization, Value: "bearer " + testAPIAuthToken},
	).Result()

	assertStatus(t, response, http.StatusCreated)
	if application.calls != 1 {
		t.Fatalf("application calls = %d, want 1", application.calls)
	}
}

func newTestSecurityMiddleware(t *testing.T) *SecurityMiddleware {
	t.Helper()
	middleware, err := NewSecurityMiddleware(testAPIAuthToken)
	if err != nil {
		t.Fatalf("NewSecurityMiddleware() error = %v", err)
	}
	return middleware
}

func assertAuthenticationProblem(t *testing.T, response *protocol.Response) {
	t.Helper()
	assertProblem(
		t,
		response,
		http.StatusUnauthorized,
		fault.CodeUnauthenticated,
		"authentication required",
	)
	if got := string(response.Header.Peek(consts.HeaderWWWAuthenticate)); got != bearerAuthenticationChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, bearerAuthenticationChallenge)
	}
	if got := string(response.Header.Peek(RequestIDHeader)); got == "" {
		t.Fatal("X-Request-ID is empty")
	}
}
