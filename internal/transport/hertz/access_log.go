package hertz

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/qiuyier/omni-agent-kit/fault"
	"github.com/qiuyier/omni-agent-kit/transport/hertz/problem"
)

const requestFaultCodeKey = "omni-agent-customer-service/request-fault-code"

// AccessLogMiddleware emits one structured record after the complete request
// lifecycle. Since Hertz and the kit SSE writer drain streams synchronously,
// an SSE duration covers the stream until completion, failure, or disconnect.
type AccessLogMiddleware struct {
	logger *zap.Logger
	now    func() time.Time
}

// recoverPanic is deliberately outside AccessLogMiddleware in the Hertz
// chain. AccessLogMiddleware records only a bounded internal classification,
// then re-panics so this middleware can finish the response without logging a
// possibly sensitive panic value or stack trace.
func recoverPanic(ctx context.Context, requestContext *app.RequestContext) {
	defer func() {
		if recover() != nil {
			requestContext.AbortWithStatus(http.StatusInternalServerError)
		}
	}()
	requestContext.Next(ctx)
}

func NewAccessLogMiddleware(logger *zap.Logger) (*AccessLogMiddleware, error) {
	return newAccessLogMiddleware(logger, time.Now)
}

func newAccessLogMiddleware(
	logger *zap.Logger,
	now func() time.Time,
) (*AccessLogMiddleware, error) {
	if logger == nil {
		return nil, invalidDependency("access logger is nil")
	}
	if now == nil {
		return nil, invalidDependency("access log clock is nil")
	}
	return &AccessLogMiddleware{logger: logger, now: now}, nil
}

func (m *AccessLogMiddleware) Log(
	ctx context.Context,
	requestContext *app.RequestContext,
) {
	startedAt := m.now()
	defer func() {
		if recovered := recover(); recovered != nil {
			m.write(ctx, requestContext, startedAt, http.StatusInternalServerError, fault.CodeInternal, true)
			panic(recovered)
		}
		m.write(
			ctx,
			requestContext,
			startedAt,
			requestContext.Response.StatusCode(),
			requestFaultCode(requestContext),
			false,
		)
	}()

	requestContext.Next(ctx)
}

func (m *AccessLogMiddleware) write(
	ctx context.Context,
	requestContext *app.RequestContext,
	startedAt time.Time,
	status int,
	faultCode fault.Code,
	panicked bool,
) {
	if status <= 0 {
		status = http.StatusOK
	}
	duration := m.now().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	route := requestContext.FullPath()
	if route == "" {
		route = "unmatched"
	}
	contentType := strings.ToLower(string(requestContext.Response.Header.ContentType()))
	fields := []zap.Field{
		zap.String("event", "http.request.completed"),
		zap.String("http_method", string(requestContext.Request.Method())),
		zap.String("http_route", route),
		zap.Int("http_status", status),
		zap.Float64("duration_ms", float64(duration.Nanoseconds())/1e6),
		zap.Bool("streaming", strings.HasPrefix(contentType, "text/event-stream")),
	}
	if requestID, ok := RequestIDFromContext(ctx); ok {
		fields = append(fields, zap.String("request_id", requestID))
	}
	if faultCode != "" {
		fields = append(fields, zap.String("fault_code", string(faultCode)))
	}
	if panicked {
		fields = append(fields, zap.Bool("panicked", true))
	}

	severityStatus := status
	if faultCode != "" && severityStatus < http.StatusBadRequest {
		// An SSE response keeps HTTP 200 after its first event. Use the safe
		// terminal fault only for severity so the access record is not mistaken
		// for a successful stream.
		severityStatus = problem.StatusCode(fault.New(faultCode, ""))
	}
	switch {
	case severityStatus >= http.StatusInternalServerError:
		m.logger.Error("http request completed", fields...)
	case severityStatus >= http.StatusBadRequest:
		m.logger.Warn("http request completed", fields...)
	default:
		m.logger.Info("http request completed", fields...)
	}
}

func rememberRequestFault(requestContext *app.RequestContext, err error) {
	if requestContext == nil || err == nil {
		return
	}
	if _, exists := requestContext.Get(requestFaultCodeKey); exists {
		return
	}
	requestContext.Set(requestFaultCodeKey, fault.CodeOf(err))
}

func requestFaultCode(requestContext *app.RequestContext) fault.Code {
	if requestContext == nil {
		return ""
	}
	value, exists := requestContext.Get(requestFaultCodeKey)
	if !exists {
		return ""
	}
	code, _ := value.(fault.Code)
	return code
}
