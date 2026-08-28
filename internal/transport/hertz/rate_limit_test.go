package hertz

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-kit/fault"
)

func TestNewRateLimitMiddlewareRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		rps   float64
		burst int
		now   func() time.Time
	}{
		{name: "zero rate", burst: 1, now: time.Now},
		{name: "negative rate", rps: -1, burst: 1, now: time.Now},
		{name: "NaN rate", rps: math.NaN(), burst: 1, now: time.Now},
		{name: "infinite rate", rps: math.Inf(1), burst: 1, now: time.Now},
		{name: "zero burst", rps: 1, now: time.Now},
		{name: "negative burst", rps: 1, burst: -1, now: time.Now},
		{name: "nil clock", rps: 1, burst: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			middleware, err := newRateLimitMiddleware(
				test.rps,
				test.burst,
				test.now,
			)
			if !errors.Is(err, errInvalidDependency) {
				t.Fatalf("newRateLimitMiddleware() error = %v, want %v", err, errInvalidDependency)
			}
			if middleware != nil {
				t.Fatalf("newRateLimitMiddleware() = %#v, want nil", middleware)
			}
		})
	}
}

func TestRateLimitReservationIsCancelledAndRecovers(t *testing.T) {
	now := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	middleware, err := newRateLimitMiddleware(2, 1, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatalf("newRateLimitMiddleware() error = %v", err)
	}

	if delay, allowed := middleware.reserve(); !allowed || delay != 0 {
		t.Fatalf("first reservation = (%v, %v), want (0, true)", delay, allowed)
	}
	if delay, allowed := middleware.reserve(); allowed || delay != 500*time.Millisecond {
		t.Fatalf("second reservation = (%v, %v), want (500ms, false)", delay, allowed)
	}

	now = now.Add(500 * time.Millisecond)
	if delay, allowed := middleware.reserve(); !allowed || delay != 0 {
		t.Fatalf("recovered reservation = (%v, %v), want (0, true)", delay, allowed)
	}
}

func TestRateLimitMiddlewareReturnsProblemAndRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	rateLimit, err := newRateLimitMiddleware(2, 1, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	application := &receiverStub{result: customerservice.ReceiveMessageResult{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		Status:         conversation.StatusBotActive,
		NextAction:     customerservice.NextActionGenerateBotReply,
	}}
	engine := newTestEngineWithRateLimit(t, application, rateLimit)

	first := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	second := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)

	assertStatus(t, first, http.StatusCreated)
	assertProblem(
		t,
		second,
		http.StatusTooManyRequests,
		fault.CodeResourceExhausted,
		"rate limit exceeded",
	)
	if got := string(second.Header.Peek(retryAfterHeader)); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
	if got := string(second.Header.Peek(RequestIDHeader)); got == "" {
		t.Fatal("rate-limit response X-Request-ID is empty")
	}
	if application.calls != 1 {
		t.Fatalf("application calls = %d, want 1", application.calls)
	}
}

func TestHealthAndInvalidCredentialsDoNotConsumeRateLimit(t *testing.T) {
	now := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	rateLimit, err := newRateLimitMiddleware(1, 1, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	application := &receiverStub{result: customerservice.ReceiveMessageResult{
		ConversationID: "conversation-1",
		MessageID:      "message-1",
		Status:         conversation.StatusBotActive,
		NextAction:     customerservice.NextActionGenerateBotReply,
	}}
	engine := newTestEngineWithRateLimit(t, application, rateLimit)

	for index := 0; index < 3; index++ {
		response := ut.PerformRequest(engine, http.MethodGet, healthPath, nil).Result()
		assertStatus(t, response, http.StatusOK)
	}
	for index := 0; index < 3; index++ {
		response := ut.PerformRequest(
			engine,
			http.MethodPost,
			receiveMessagePath,
			&ut.Body{Body: strings.NewReader(validReceiveMessageJSON), Len: len(validReceiveMessageJSON)},
			ut.Header{Key: consts.HeaderAuthorization, Value: "Bearer wrong-token"},
		).Result()
		assertAuthenticationProblem(t, response)
	}

	accepted := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	rejected := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	assertStatus(t, accepted, http.StatusCreated)
	assertProblem(
		t,
		rejected,
		http.StatusTooManyRequests,
		fault.CodeResourceExhausted,
		"rate limit exceeded",
	)
	if application.calls != 1 {
		t.Fatalf("application calls = %d, want 1", application.calls)
	}
}

func TestAllProtectedRoutesShareRateLimit(t *testing.T) {
	now := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	rateLimit, err := newRateLimitMiddleware(1, 2, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	application := &receiverStub{
		result: customerservice.ReceiveMessageResult{
			ConversationID: "conversation-1",
			MessageID:      "message-1",
			Status:         conversation.StatusBotActive,
			NextAction:     customerservice.NextActionGenerateBotReply,
		},
		replyResult: customerservice.GenerateBotReplyResult{
			ConversationID:   "conversation-1",
			TriggerMessageID: "message-1",
			MessageID:        "message-2",
			Content:          "answer",
		},
	}
	engine := newTestEngineWithRateLimit(t, application, rateLimit)

	receive := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	reply := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages/message-1/reply",
		nil,
	)
	stream := performAuthorizedRequest(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages/message-1/reply/stream",
		nil,
	)

	assertStatus(t, receive, http.StatusCreated)
	assertStatus(t, reply, http.StatusCreated)
	assertProblem(
		t,
		stream,
		http.StatusTooManyRequests,
		fault.CodeResourceExhausted,
		"rate limit exceeded",
	)
	if application.calls != 1 || application.replyCalls != 1 || application.streamCalls != 0 {
		t.Fatalf(
			"application calls = receive:%d reply:%d stream:%d, want 1,1,0",
			application.calls,
			application.replyCalls,
			application.streamCalls,
		)
	}
}

func TestRateLimitMiddlewareConcurrentBurst(t *testing.T) {
	const (
		burst        = 10
		requestCount = 100
	)
	now := time.Date(2026, time.August, 27, 9, 0, 0, 0, time.UTC)
	middleware, err := newRateLimitMiddleware(1, burst, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}

	var allowed atomic.Int64
	var waitGroup sync.WaitGroup
	waitGroup.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		go func() {
			defer waitGroup.Done()
			if _, ok := middleware.reserve(); ok {
				allowed.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := allowed.Load(); got != burst {
		t.Fatalf("allowed requests = %d, want %d", got, burst)
	}
	now = now.Add(time.Second)
	if delay, ok := middleware.reserve(); !ok || delay != 0 {
		t.Fatalf("reservation after refill = (%v, %v), want (0, true)", delay, ok)
	}
}

func TestRetryAfterSecondsRoundsUp(t *testing.T) {
	tests := []struct {
		delay time.Duration
		want  int64
	}{
		{delay: 0, want: 1},
		{delay: 500 * time.Millisecond, want: 1},
		{delay: time.Second, want: 1},
		{delay: 2500 * time.Millisecond, want: 3},
	}
	for _, test := range tests {
		if got := retryAfterSeconds(test.delay); got != test.want {
			t.Fatalf("retryAfterSeconds(%v) = %d, want %d", test.delay, got, test.want)
		}
	}
}
