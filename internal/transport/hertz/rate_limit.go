package hertz

import (
	"context"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"golang.org/x/time/rate"

	"github.com/qiuyier/omni-agent-kit/fault"
)

const retryAfterHeader = "Retry-After"

// RateLimitMiddleware applies one process-local token bucket shared by all
// authenticated /v1 requests. It does not limit health checks or SSE events
// emitted after a stream has already been admitted.
type RateLimitMiddleware struct {
	mu      sync.Mutex
	limiter *rate.Limiter
	now     func() time.Time
}

// NewRateLimitMiddleware creates a token bucket that starts with burst tokens.
func NewRateLimitMiddleware(
	requestsPerSecond float64,
	burst int,
) (*RateLimitMiddleware, error) {
	return newRateLimitMiddleware(requestsPerSecond, burst, time.Now)
}

func newRateLimitMiddleware(
	requestsPerSecond float64,
	burst int,
	now func() time.Time,
) (*RateLimitMiddleware, error) {
	if requestsPerSecond <= 0 ||
		math.IsNaN(requestsPerSecond) ||
		math.IsInf(requestsPerSecond, 0) {
		return nil, invalidDependency("API rate limit must be a positive finite number")
	}
	if burst <= 0 {
		return nil, invalidDependency("API rate limit burst must be positive")
	}
	if now == nil {
		return nil, invalidDependency("rate limit clock is nil")
	}

	return &RateLimitMiddleware{
		limiter: rate.NewLimiter(rate.Limit(requestsPerSecond), burst),
		now:     now,
	}, nil
}

// Limit admits an immediately available token or returns a client-safe 429.
// It never waits because queued HTTP and SSE requests would consume resources.
func (m *RateLimitMiddleware) Limit(
	ctx context.Context,
	requestContext *app.RequestContext,
) {
	retryAfter, allowed := m.reserve()
	if allowed {
		requestContext.Next(ctx)
		return
	}

	requestContext.Response.Header.Set(
		retryAfterHeader,
		strconv.FormatInt(retryAfterSeconds(retryAfter), 10),
	)
	writeProblem(
		requestContext,
		fault.New(fault.CodeResourceExhausted, "rate limit exceeded"),
	)
}

func (m *RateLimitMiddleware) reserve() (time.Duration, bool) {
	if m == nil || m.limiter == nil || m.now == nil {
		return time.Second, false
	}

	// ReserveN, DelayFrom, and CancelAt form one logical operation. The outer
	// mutex prevents concurrent future reservations from being cancelled out of
	// order and leaving tokens reserved for requests that were already rejected.
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	reservation := m.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return time.Second, false
	}
	delay := reservation.DelayFrom(now)
	if delay <= 0 {
		return 0, true
	}

	reservation.CancelAt(now)
	return delay, false
}

func retryAfterSeconds(delay time.Duration) int64 {
	seconds := delay / time.Second
	if delay%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return int64(seconds)
}
