package hertz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-kit/fault"
)

func TestNewAccessLogMiddlewareRejectsInvalidDependencies(t *testing.T) {
	if middleware, err := NewAccessLogMiddleware(nil); !errors.Is(err, errInvalidDependency) || middleware != nil {
		t.Fatalf("NewAccessLogMiddleware(nil) = %#v, %v", middleware, err)
	}
	if middleware, err := newAccessLogMiddleware(zap.NewNop(), nil); !errors.Is(err, errInvalidDependency) || middleware != nil {
		t.Fatalf("newAccessLogMiddleware(logger, nil) = %#v, %v", middleware, err)
	}
}

func TestAccessLogUsesRouteTemplateAndSafeRequestFields(t *testing.T) {
	application := &receiverStub{replyResult: customerservice.GenerateBotReplyResult{
		ConversationID:   "conversation-from-service",
		TriggerMessageID: "message-from-service",
		MessageID:        "reply-1",
		Content:          "answer",
	}}
	engine, logs := newObservedAccessLogEngine(t, application)
	actualConversationID := "customer-secret-conversation"
	actualTriggerID := "customer-secret-message"

	response := performAuthorizedRequest(
		engine,
		http.MethodPost,
		fmt.Sprintf("/v1/conversations/%s/messages/%s/reply", actualConversationID, actualTriggerID),
		nil,
		ut.Header{Key: RequestIDHeader, Value: "request-123"},
	)
	assertStatus(t, response, http.StatusCreated)

	entry := takeSingleAccessLog(t, logs)
	if entry.Level != zapcore.InfoLevel || entry.Message != "http request completed" {
		t.Fatalf("entry level/message = %s/%q", entry.Level, entry.Message)
	}
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "event", "http.request.completed")
	assertAccessLogField(t, fields, "request_id", "request-123")
	assertAccessLogField(t, fields, "http_method", http.MethodPost)
	assertAccessLogField(t, fields, "http_route", generateReplyPath)
	assertAccessLogField(t, fields, "http_status", int64(http.StatusCreated))
	assertAccessLogField(t, fields, "streaming", false)
	if duration, ok := fields["duration_ms"].(float64); !ok || duration < 0 {
		t.Fatalf("duration_ms = %#v, want non-negative float64", fields["duration_ms"])
	}
	assertLogOmits(t, entry, actualConversationID, actualTriggerID, testAPIAuthToken)
}

func TestAccessLogRecordsSafeFaultWithoutCredentialsOrProviderErrors(t *testing.T) {
	t.Run("authentication", func(t *testing.T) {
		engine, logs := newObservedAccessLogEngine(t, &receiverStub{})
		secretToken := "bearer-secret-must-not-appear"
		response := ut.PerformRequest(
			engine,
			http.MethodPost,
			"/v1/conversations/conversation-1/messages",
			nil,
			ut.Header{Key: "Authorization", Value: "Bearer " + secretToken},
			ut.Header{Key: RequestIDHeader, Value: "request-auth"},
		).Result()
		assertStatus(t, response, http.StatusUnauthorized)

		entry := takeSingleAccessLog(t, logs)
		if entry.Level != zapcore.WarnLevel {
			t.Fatalf("entry level = %s, want warn", entry.Level)
		}
		fields := entry.ContextMap()
		assertAccessLogField(t, fields, "http_status", int64(http.StatusUnauthorized))
		assertAccessLogField(t, fields, "fault_code", string(fault.CodeUnauthenticated))
		assertLogOmits(t, entry, secretToken)
	})

	t.Run("provider failure", func(t *testing.T) {
		providerDetail := "api_key=sk-provider-secret prompt=private-customer-message"
		application := &receiverStub{replyErr: fmt.Errorf(
			"%w: %s",
			customerservice.ErrReplyGenerationFailed,
			providerDetail,
		)}
		engine, logs := newObservedAccessLogEngine(t, application)
		response := performAuthorizedRequest(
			engine,
			http.MethodPost,
			"/v1/conversations/conversation-1/messages/message-1/reply",
			nil,
		)
		assertStatus(t, response, http.StatusServiceUnavailable)

		entry := takeSingleAccessLog(t, logs)
		if entry.Level != zapcore.ErrorLevel {
			t.Fatalf("entry level = %s, want error", entry.Level)
		}
		fields := entry.ContextMap()
		assertAccessLogField(t, fields, "http_status", int64(http.StatusServiceUnavailable))
		assertAccessLogField(t, fields, "fault_code", string(fault.CodeUnavailable))
		assertLogOmits(t, entry, providerDetail, "sk-provider-secret", "private-customer-message")
	})
}

func TestAccessLogClassifiesDrainingReadinessWithoutSensitiveData(t *testing.T) {
	availability := NewAvailability()
	engine, logs := newObservedAccessLogEngineWithAvailability(
		t,
		&receiverStub{},
		availability,
	)
	availability.BeginDraining()

	response := ut.PerformRequest(
		engine,
		http.MethodGet,
		readinessPath,
		nil,
		ut.Header{Key: RequestIDHeader, Value: "request-draining"},
		ut.Header{Key: "Authorization", Value: "Bearer secret-must-not-log"},
	).Result()
	assertStatus(t, response, http.StatusServiceUnavailable)

	entry := takeSingleAccessLog(t, logs)
	if entry.Level != zapcore.ErrorLevel {
		t.Fatalf("entry level = %s, want error", entry.Level)
	}
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "request_id", "request-draining")
	assertAccessLogField(t, fields, "http_route", readinessPath)
	assertAccessLogField(t, fields, "http_status", int64(http.StatusServiceUnavailable))
	assertAccessLogField(t, fields, "fault_code", string(fault.CodeUnavailable))
	assertLogOmits(t, entry, "secret-must-not-log")
}

func TestAccessLogUsesUnmatchedForUnknownPaths(t *testing.T) {
	engine, logs := newObservedAccessLogEngine(t, &receiverStub{})
	rawPathDetail := "alice@example.com"
	querySecret := "query-api-key-secret"
	response := ut.PerformRequest(
		engine,
		http.MethodGet,
		"/customers/"+rawPathDetail+"?api_key="+querySecret,
		nil,
	).Result()
	assertStatus(t, response, http.StatusNotFound)

	entry := takeSingleAccessLog(t, logs)
	if entry.Level != zapcore.WarnLevel {
		t.Fatalf("entry level = %s, want warn", entry.Level)
	}
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "http_route", "unmatched")
	assertAccessLogField(t, fields, "http_status", int64(http.StatusNotFound))
	assertLogOmits(t, entry, rawPathDetail, querySecret)
}

func TestAccessLogWaitsForCompleteStreamingLifecycle(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	middleware, err := NewAccessLogMiddleware(zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	security := newTestSecurityMiddleware(t)
	started := make(chan struct{})
	release := make(chan struct{})
	httpServer := server.New()
	httpServer.Use(security.RequestID, middleware.Log)
	httpServer.GET("/stream/:conversation_id", func(
		_ context.Context,
		requestContext *app.RequestContext,
	) {
		requestContext.Response.Header.Set("Content-Type", "text/event-stream")
		close(started)
		<-release
		requestContext.SetStatusCode(http.StatusOK)
	})

	responseDone := make(chan *protocol.Response, 1)
	go func() {
		responseDone <- ut.PerformRequest(
			httpServer.Engine,
			http.MethodGet,
			"/stream/private-conversation-id",
			nil,
		).Result()
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not start")
	}
	if count := logs.Len(); count != 0 {
		t.Fatalf("access logs while stream active = %d, want 0", count)
	}
	close(release)
	select {
	case response := <-responseDone:
		assertStatus(t, response, http.StatusOK)
	case <-time.After(time.Second):
		t.Fatal("stream request did not finish")
	}

	entry := takeSingleAccessLog(t, logs)
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "http_route", "/stream/:conversation_id")
	assertAccessLogField(t, fields, "streaming", true)
	assertLogOmits(t, entry, "private-conversation-id")
}

func TestAccessLogUsesTerminalSSEFaultForSeverityAfterHTTP200(t *testing.T) {
	providerDetail := "provider api_key=sse-secret prompt=private-stream-message"
	core, logs := observer.New(zapcore.DebugLevel)
	middleware, err := NewAccessLogMiddleware(zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	security := newTestSecurityMiddleware(t)
	httpServer := server.New()
	httpServer.Use(recoverPanic, security.RequestID, middleware.Log)
	httpServer.GET("/stream/:conversation_id", func(
		_ context.Context,
		requestContext *app.RequestContext,
	) {
		requestContext.Response.Header.Set("Content-Type", "text/event-stream")
		requestContext.SetStatusCode(http.StatusOK)
		rememberRequestFault(requestContext, fault.Wrap(
			fault.CodeUnavailable,
			"reply service unavailable",
			errors.New(providerDetail),
		))
	})

	response := ut.PerformRequest(
		httpServer.Engine,
		http.MethodGet,
		"/stream/private-conversation-id",
		nil,
	).Result()
	assertStatus(t, response, http.StatusOK)

	entry := takeSingleAccessLog(t, logs)
	if entry.Level != zapcore.ErrorLevel {
		t.Fatalf("entry level = %s, want error for terminal SSE fault", entry.Level)
	}
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "http_status", int64(http.StatusOK))
	assertAccessLogField(t, fields, "streaming", true)
	assertAccessLogField(t, fields, "fault_code", string(fault.CodeUnavailable))
	assertLogOmits(t, entry, providerDetail, "sse-secret", "private-stream-message")
}

func TestAccessLogRecordsPanicSafelyThenLetsRecoveryHandleIt(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	middleware, err := NewAccessLogMiddleware(zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	security := newTestSecurityMiddleware(t)
	httpServer := server.New()
	httpServer.Use(recoverPanic, security.RequestID, middleware.Log)
	panicDetail := "panic api_key=never-log-this"
	httpServer.GET("/panic", func(context.Context, *app.RequestContext) {
		panic(panicDetail)
	})

	response := ut.PerformRequest(httpServer.Engine, http.MethodGet, "/panic", nil).Result()
	assertStatus(t, response, http.StatusInternalServerError)
	entry := takeSingleAccessLog(t, logs)
	if entry.Level != zapcore.ErrorLevel {
		t.Fatalf("entry level = %s, want error", entry.Level)
	}
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "http_status", int64(http.StatusInternalServerError))
	assertAccessLogField(t, fields, "fault_code", string(fault.CodeInternal))
	assertAccessLogField(t, fields, "panicked", true)
	assertLogOmits(t, entry, panicDetail, "never-log-this")
}

func TestRememberRequestFaultKeepsFirstClassification(t *testing.T) {
	requestContext := &app.RequestContext{}
	rememberRequestFault(requestContext, fault.New(fault.CodeUnauthenticated, ""))
	rememberRequestFault(requestContext, fault.New(fault.CodeInternal, ""))
	if code := requestFaultCode(requestContext); code != fault.CodeUnauthenticated {
		t.Fatalf("request fault code = %q, want first code %q", code, fault.CodeUnauthenticated)
	}
}

func newObservedAccessLogEngine(
	t *testing.T,
	application Application,
) (*route.Engine, *observer.ObservedLogs) {
	t.Helper()
	return newObservedAccessLogEngineWithAvailability(t, application, NewAvailability())
}

func newObservedAccessLogEngineWithAvailability(
	t *testing.T,
	application Application,
	availability *Availability,
) (*route.Engine, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	accessLog, err := NewAccessLogMiddleware(zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(application)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := server.New()
	if err := RegisterRoutes(
		httpServer.Engine,
		handler,
		availability,
		newTestSecurityMiddleware(t),
		accessLog,
		newTestRateLimitMiddleware(t),
	); err != nil {
		t.Fatal(err)
	}
	return httpServer.Engine, logs
}

func takeSingleAccessLog(t *testing.T, logs *observer.ObservedLogs) observer.LoggedEntry {
	t.Helper()
	entries := logs.TakeAll()
	if len(entries) != 1 {
		t.Fatalf("access log entries = %d, want 1", len(entries))
	}
	return entries[0]
}

func assertAccessLogField(t *testing.T, fields map[string]any, key string, want any) {
	t.Helper()
	if got := fields[key]; got != want {
		t.Fatalf("log field %q = %#v, want %#v; fields = %#v", key, got, want, fields)
	}
}

func assertLogOmits(t *testing.T, entry observer.LoggedEntry, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Message string         `json:"message"`
		Fields  map[string]any `json:"fields"`
	}{
		Message: entry.Message,
		Fields:  entry.ContextMap(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if value != "" && containsString(encoded, value) {
			t.Fatalf("access log leaked %q: %s", value, encoded)
		}
	}
}

func containsString(value []byte, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if string(value[index:index+len(fragment)]) == fragment {
			return true
		}
	}
	return false
}
