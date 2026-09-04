package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	zapobserver "go.uber.org/zap/zaptest/observer"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/observability"
)

type observerContextKey string

const (
	requestIDContextKey observerContextKey = "request-id"
	customerContextKey  observerContextKey = "customer-message"
	apiKeyContextKey    observerContextKey = "api-key"
	providerContextKey  observerContextKey = "provider-error"
)

func TestNewZapReplyObserverRejectsNilLogger(t *testing.T) {
	observer, err := observability.NewZapReplyObserver(nil, nil)
	if err == nil {
		t.Fatal("NewZapReplyObserver() error = nil, want dependency error")
	}
	if observer != nil {
		t.Fatal("NewZapReplyObserver() returned observer for nil logger")
	}
}

func TestZapReplyObserverEmitsStructuredLifecycleEventsWithoutSensitiveData(t *testing.T) {
	requestID := "request-123"
	observer, logs := newObservedReplyObserver(t, func(ctx context.Context) (string, bool) {
		value, ok := ctx.Value(requestIDContextKey).(string)
		return value, ok
	})

	const (
		customerMessage = "CUSTOMER_MESSAGE_DO_NOT_LOG"
		apiKey          = "sk-API_KEY_DO_NOT_LOG"
		providerError   = "PROVIDER_RAW_ERROR_DO_NOT_LOG"
	)
	ctx := context.WithValue(context.Background(), requestIDContextKey, requestID)
	ctx = context.WithValue(ctx, customerContextKey, customerMessage)
	ctx = context.WithValue(ctx, apiKeyContextKey, apiKey)
	ctx = context.WithValue(ctx, providerContextKey, errors.New(providerError))

	observer.ReplyStarted(ctx, customerservice.ReplyStartedEvent{
		Operation: customerservice.ReplyOperation{
			Mode:             customerservice.ReplyModeGenerate,
			ConversationID:   "conversation-1",
			TriggerMessageID: "message-1",
		},
		Capacity: customerservice.ReplyCapacitySnapshot{Active: 1, Limit: 5},
	})
	observer.ReplyRejected(ctx, customerservice.ReplyRejectedEvent{
		Operation: customerservice.ReplyOperation{
			Mode:             customerservice.ReplyModeStream,
			ConversationID:   "conversation-2",
			TriggerMessageID: "message-2",
		},
		Capacity: customerservice.ReplyCapacitySnapshot{Active: 5, Limit: 5},
	})
	observer.ReplyFinished(ctx, customerservice.ReplyFinishedEvent{
		Operation: customerservice.ReplyOperation{
			Mode:             customerservice.ReplyModeStream,
			ConversationID:   "conversation-3",
			TriggerMessageID: "message-3",
		},
		Outcome:       customerservice.ReplyOutcomeGenerationFailed,
		Phase:         customerservice.ReplyPhaseReceive,
		Capacity:      customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 5},
		ModelDuration: 12*time.Millisecond + 500*time.Microsecond,
		TotalDuration: 20*time.Millisecond + 750*time.Microsecond,
		Duplicate:     true,
		CleanupFailed: true,
	})

	entries := logs.All()
	if len(entries) != 3 {
		t.Fatalf("log count = %d, want 3", len(entries))
	}
	assertObservedEntry(t, entries[0], zapcore.InfoLevel, "ai reply started", map[string]any{
		"request_id":             requestID,
		"event":                  "ai.reply.started",
		"reply_mode":             "generate",
		"conversation_id":        "conversation-1",
		"trigger_message_id":     "message-1",
		"active_replies":         1,
		"max_concurrent_replies": 5,
	})
	assertObservedEntry(t, entries[1], zapcore.WarnLevel, "ai reply rejected", map[string]any{
		"request_id":             requestID,
		"event":                  "ai.reply.rejected",
		"reply_mode":             "stream",
		"conversation_id":        "conversation-2",
		"trigger_message_id":     "message-2",
		"active_replies":         5,
		"max_concurrent_replies": 5,
		"outcome":                "capacity_exceeded",
	})
	assertObservedEntry(t, entries[2], zapcore.ErrorLevel, "ai reply finished", map[string]any{
		"request_id":             requestID,
		"event":                  "ai.reply.finished",
		"reply_mode":             "stream",
		"conversation_id":        "conversation-3",
		"trigger_message_id":     "message-3",
		"active_replies":         0,
		"max_concurrent_replies": 5,
		"outcome":                "generation_failed",
		"terminal_phase":         "receive",
		"model_duration_ms":      12.5,
		"total_duration_ms":      20.75,
		"duplicate":              true,
		"cleanup_failed":         true,
	})

	var rendered strings.Builder
	for _, entry := range entries {
		rendered.WriteString(entry.Message)
		fields, err := json.Marshal(entry.ContextMap())
		if err != nil {
			t.Fatalf("marshal observed fields: %v", err)
		}
		rendered.Write(fields)
	}
	for _, sensitive := range []string{customerMessage, apiKey, providerError} {
		if strings.Contains(rendered.String(), sensitive) {
			t.Fatalf("structured reply logs leaked sensitive marker %q: %s", sensitive, rendered.String())
		}
	}
}

func TestZapReplyObserverMapsOutcomesToBoundedLevels(t *testing.T) {
	tests := []struct {
		name          string
		outcome       customerservice.ReplyOutcome
		cleanupFailed bool
		level         zapcore.Level
	}{
		{name: "completed", outcome: customerservice.ReplyOutcomeCompleted, level: zapcore.InfoLevel},
		{name: "completed with cleanup failure", outcome: customerservice.ReplyOutcomeCompleted, cleanupFailed: true, level: zapcore.WarnLevel},
		{name: "superseded", outcome: customerservice.ReplyOutcomeSuperseded, level: zapcore.InfoLevel},
		{name: "empty", outcome: customerservice.ReplyOutcomeEmpty, level: zapcore.WarnLevel},
		{name: "generation failed", outcome: customerservice.ReplyOutcomeGenerationFailed, level: zapcore.ErrorLevel},
		{name: "persistence failed", outcome: customerservice.ReplyOutcomePersistenceFailed, level: zapcore.ErrorLevel},
		{name: "canceled", outcome: customerservice.ReplyOutcomeCanceled, level: zapcore.InfoLevel},
		{name: "deadline exceeded", outcome: customerservice.ReplyOutcomeDeadlineExceeded, level: zapcore.WarnLevel},
		{name: "abandoned", outcome: customerservice.ReplyOutcomeAbandoned, level: zapcore.InfoLevel},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observer, logs := newObservedReplyObserver(t, nil)
			observer.ReplyFinished(context.Background(), customerservice.ReplyFinishedEvent{
				Operation: customerservice.ReplyOperation{
					Mode:             customerservice.ReplyModeGenerate,
					ConversationID:   "conversation-1",
					TriggerMessageID: "message-1",
				},
				Outcome:       test.outcome,
				Phase:         customerservice.ReplyPhasePersist,
				Capacity:      customerservice.ReplyCapacitySnapshot{Active: 0, Limit: 5},
				CleanupFailed: test.cleanupFailed,
			})

			entries := logs.All()
			if len(entries) != 1 {
				t.Fatalf("log count = %d, want 1", len(entries))
			}
			if entries[0].Level != test.level {
				t.Errorf("log level = %v, want %v", entries[0].Level, test.level)
			}
			if got := entries[0].ContextMap()["outcome"]; got != string(test.outcome) {
				t.Errorf("outcome = %#v, want %q", got, test.outcome)
			}
			if got, exists := entries[0].ContextMap()["cleanup_failed"]; exists != test.cleanupFailed ||
				(test.cleanupFailed && got != true) {
				t.Errorf("cleanup_failed = %#v (exists %v), want present=%v", got, exists, test.cleanupFailed)
			}
			if _, exists := entries[0].ContextMap()["request_id"]; exists {
				t.Error("request_id logged without an extractor")
			}
		})
	}
}

func newObservedReplyObserver(
	t *testing.T,
	extractor observability.RequestIDExtractor,
) (*observability.ZapReplyObserver, *zapobserver.ObservedLogs) {
	t.Helper()
	core, logs := zapobserver.New(zapcore.DebugLevel)
	observer, err := observability.NewZapReplyObserver(zap.New(core), extractor)
	if err != nil {
		t.Fatalf("NewZapReplyObserver() error = %v", err)
	}
	return observer, logs
}

func assertObservedEntry(
	t *testing.T,
	entry zapobserver.LoggedEntry,
	level zapcore.Level,
	message string,
	wantFields map[string]any,
) {
	t.Helper()
	if entry.Level != level {
		t.Errorf("log level = %v, want %v", entry.Level, level)
	}
	if entry.Message != message {
		t.Errorf("log message = %q, want %q", entry.Message, message)
	}
	gotFields := entry.ContextMap()
	for name, want := range wantFields {
		if got := gotFields[name]; !observedValuesEqual(got, want) {
			t.Errorf("log field %q = %#v, want %#v", name, got, want)
		}
	}
}

func observedValuesEqual(got, want any) bool {
	wantInt, wantsInt := want.(int)
	if !wantsInt {
		return reflect.DeepEqual(got, want)
	}
	switch value := got.(type) {
	case int:
		return value == wantInt
	case int32:
		return int64(value) == int64(wantInt)
	case int64:
		return value == int64(wantInt)
	default:
		return false
	}
}
