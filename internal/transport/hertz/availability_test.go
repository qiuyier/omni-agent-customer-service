package hertz

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz/model"
	"github.com/qiuyier/omni-agent-kit/fault"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestAvailabilityProbeAndAdmissionLifecycle(t *testing.T) {
	availability := NewAvailability()
	frozenTime := time.Date(2026, time.September, 5, 9, 0, 0, 0, time.UTC)
	rateLimit, err := newRateLimitMiddleware(1, 1, func() time.Time {
		return frozenTime
	})
	if err != nil {
		t.Fatal(err)
	}
	application := &receiverStub{}
	engine := newAvailabilityTestEngine(t, application, availability, rateLimit)

	readyResponse := ut.PerformRequest(engine, http.MethodGet, readinessPath, nil).Result()
	assertStatus(t, readyResponse, http.StatusOK)
	assertContentType(t, readyResponse, "application/json; charset=utf-8")
	if requestID := string(readyResponse.Header.Peek(RequestIDHeader)); requestID == "" {
		t.Fatal("readiness response X-Request-ID is empty")
	}
	var readyBody model.HealthResponse
	decodeResponse(t, readyResponse, &readyBody)
	if readyBody.Status != "ready" {
		t.Fatalf("readiness status = %q, want ready", readyBody.Status)
	}

	if !availability.BeginDraining() {
		t.Fatal("first BeginDraining() = false, want true")
	}
	if availability.BeginDraining() {
		t.Fatal("second BeginDraining() = true, want false")
	}

	drainingResponse := ut.PerformRequest(engine, http.MethodGet, readinessPath, nil).Result()
	assertProblem(
		t,
		drainingResponse,
		http.StatusServiceUnavailable,
		fault.CodeUnavailable,
		"service unavailable",
	)
	if requestID := string(drainingResponse.Header.Peek(RequestIDHeader)); requestID == "" {
		t.Fatal("draining readiness response X-Request-ID is empty")
	}

	healthResponse := ut.PerformRequest(
		engine,
		http.MethodGet,
		healthPath,
		nil,
		ut.Header{Key: "Authorization", Value: "Bearer wrong-token"},
	).Result()
	assertStatus(t, healthResponse, http.StatusOK)

	// A request without credentials proving unavailable rather than
	// unauthenticated locks the draining gate before authentication.
	businessResponse := ut.PerformRequest(
		engine,
		http.MethodPost,
		receiveMessagePath,
		nil,
	).Result()
	assertProblem(
		t,
		businessResponse,
		http.StatusServiceUnavailable,
		fault.CodeUnavailable,
		"service unavailable",
	)
	// A separately authorized request must also stop before the one-token rate
	// limiter. The direct reservation below then proves the token is untouched.
	authorizedResponse := performJSON(
		engine,
		http.MethodPost,
		"/v1/conversations/conversation-1/messages",
		validReceiveMessageJSON,
	)
	assertProblem(
		t,
		authorizedResponse,
		http.StatusServiceUnavailable,
		fault.CodeUnavailable,
		"service unavailable",
	)
	if application.calls != 0 {
		t.Fatalf("application calls = %d, want 0", application.calls)
	}
	if delay, allowed := rateLimit.reserve(); !allowed || delay != 0 {
		t.Fatalf("first rate-limit reservation = (%v, %v), want (0, true)", delay, allowed)
	}

	availability.MarkStopped()
	if availability.IsReady() {
		t.Fatal("IsReady() after MarkStopped = true, want false")
	}
	stoppedResponse := ut.PerformRequest(engine, http.MethodGet, readinessPath, nil).Result()
	assertProblem(
		t,
		stoppedResponse,
		http.StatusServiceUnavailable,
		fault.CodeUnavailable,
		"service unavailable",
	)
}

func TestAvailabilityBeginDrainingHasOneConcurrentWinner(t *testing.T) {
	const callers = 64
	availability := NewAvailability()
	var winners atomic.Int64
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for range callers {
		go func() {
			defer waitGroup.Done()
			if availability.BeginDraining() {
				winners.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("BeginDraining winners = %d, want 1", got)
	}
	if availability.IsReady() {
		t.Fatal("IsReady() after BeginDraining = true, want false")
	}
}

func TestDrainingLetsAdmittedRequestFinishAndRejectsNewRequest(t *testing.T) {
	availability := NewAvailability()
	application := newBlockingApplication()
	t.Cleanup(application.releaseRequest)
	engine := newAvailabilityTestEngine(
		t,
		application,
		availability,
		newTestRateLimitMiddleware(t),
	)

	firstResponse := make(chan int, 1)
	go func() {
		response := performJSON(
			engine,
			http.MethodPost,
			"/v1/conversations/conversation-1/messages",
			validReceiveMessageJSON,
		)
		firstResponse <- response.StatusCode()
	}()

	select {
	case <-application.entered:
	case <-time.After(time.Second):
		t.Fatal("admitted request did not enter application")
	}
	if !availability.BeginDraining() {
		t.Fatal("BeginDraining() = false, want true")
	}
	secondResponses := make(chan *protocol.Response, 1)
	go func() {
		secondResponses <- performJSON(
			engine,
			http.MethodPost,
			"/v1/conversations/conversation-1/messages",
			validReceiveMessageJSON,
		)
	}()
	var secondResponse *protocol.Response
	select {
	case secondResponse = <-secondResponses:
	case <-time.After(time.Second):
		application.releaseRequest()
		t.Fatal("new request was not rejected while draining")
	}
	assertProblem(
		t,
		secondResponse,
		http.StatusServiceUnavailable,
		fault.CodeUnavailable,
		"service unavailable",
	)

	application.releaseRequest()
	var status int
	select {
	case status = <-firstResponse:
	case <-time.After(time.Second):
		t.Fatal("admitted request did not finish after release")
	}
	if status != http.StatusCreated {
		t.Fatalf("admitted response status = %d, want %d", status, http.StatusCreated)
	}
	if calls := application.calls.Load(); calls != 1 {
		t.Fatalf("application calls = %d, want 1", calls)
	}
}

type blockingApplication struct {
	entered     chan struct{}
	release     chan struct{}
	calls       atomic.Int64
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingApplication() *blockingApplication {
	return &blockingApplication{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (application *blockingApplication) ReceiveMessage(
	ctx context.Context,
	_ customerservice.ReceiveMessageCommand,
) (customerservice.ReceiveMessageResult, error) {
	application.calls.Add(1)
	application.enterOnce.Do(func() { close(application.entered) })
	select {
	case <-application.release:
		return customerservice.ReceiveMessageResult{
			ConversationID: "conversation-1",
			MessageID:      "message-1",
			Status:         conversation.StatusBotActive,
			NextAction:     customerservice.NextActionGenerateBotReply,
		}, nil
	case <-ctx.Done():
		return customerservice.ReceiveMessageResult{}, ctx.Err()
	}
}

func (application *blockingApplication) releaseRequest() {
	application.releaseOnce.Do(func() { close(application.release) })
}

func (*blockingApplication) GenerateBotReply(
	context.Context,
	customerservice.GenerateBotReplyCommand,
) (customerservice.GenerateBotReplyResult, error) {
	return customerservice.GenerateBotReplyResult{}, errors.New("unexpected GenerateBotReply call")
}

func (*blockingApplication) StreamBotReply(
	context.Context,
	customerservice.GenerateBotReplyCommand,
) (kitstream.Reader[customerservice.BotReplyStreamEvent], error) {
	return nil, errors.New("unexpected StreamBotReply call")
}

func newAvailabilityTestEngine(
	t *testing.T,
	application Application,
	availability *Availability,
	rateLimit *RateLimitMiddleware,
) *route.Engine {
	t.Helper()
	handler, err := NewHandler(application)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	httpServer := server.New()
	if err := RegisterRoutes(
		httpServer.Engine,
		handler,
		availability,
		newTestSecurityMiddleware(t),
		newTestAccessLogMiddleware(t),
		rateLimit,
	); err != nil {
		t.Fatalf("RegisterRoutes() error = %v", err)
	}
	return httpServer.Engine
}
