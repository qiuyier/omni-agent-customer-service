package hertz

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz/model"
	"github.com/qiuyier/omni-agent-kit/fault"
)

type availabilityState uint32

const (
	availabilityReady availabilityState = iota
	availabilityDraining
	availabilityStopped
)

// Availability is a process-local, one-way HTTP admission state machine.
// Requests that pass RequireReady remain admitted; later requests observe the
// new state and are rejected before authentication, rate limiting, or business
// logic. Hertz remains responsible for waiting for already-active requests.
type Availability struct {
	state atomic.Uint32
}

// NewAvailability starts ready because cmd/server constructs it only after all
// mandatory dependencies have initialized successfully.
func NewAvailability() *Availability {
	return &Availability{}
}

// BeginDraining changes ready to draining exactly once. It returns true only
// to the caller that performed the transition.
func (a *Availability) BeginDraining() bool {
	if a == nil {
		return false
	}
	return a.state.CompareAndSwap(
		uint32(availabilityReady),
		uint32(availabilityDraining),
	)
}

// MarkStopped records the terminal lifecycle state. A stopped process can
// never become ready again.
func (a *Availability) MarkStopped() {
	if a == nil {
		return
	}
	a.state.Store(uint32(availabilityStopped))
}

// IsReady is safe for concurrent probe, middleware, and signal goroutines.
// A nil receiver fails closed.
func (a *Availability) IsReady() bool {
	return a != nil && availabilityState(a.state.Load()) == availabilityReady
}

// Readiness reports whether the process should receive new business traffic.
// Unlike /healthz, it intentionally fails while the process is draining.
func (a *Availability) Readiness(
	_ context.Context,
	requestContext *app.RequestContext,
) {
	if !a.IsReady() {
		writeProblem(requestContext, fault.New(fault.CodeUnavailable, ""))
		return
	}
	requestContext.JSON(http.StatusOK, &model.HealthResponse{Status: "ready"})
}

// RequireReady is the first /v1 middleware. It prevents new work from
// consuming authentication, rate-limit, database, or model resources after
// graceful shutdown begins.
func (a *Availability) RequireReady(
	ctx context.Context,
	requestContext *app.RequestContext,
) {
	if !a.IsReady() {
		writeProblem(requestContext, fault.New(fault.CodeUnavailable, ""))
		return
	}
	requestContext.Next(ctx)
}
