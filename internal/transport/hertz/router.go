package hertz

import "github.com/cloudwego/hertz/pkg/route"

const (
	apiPrefix           = "/v1"
	healthPath          = "/healthz"
	readinessPath       = "/readyz"
	receiveMessageRoute = "/conversations/:conversation_id/messages"
	generateReplyRoute  = "/conversations/:conversation_id/messages/:trigger_message_id/reply"
	streamReplyRoute    = "/conversations/:conversation_id/messages/:trigger_message_id/reply/stream"
	receiveMessagePath  = apiPrefix + receiveMessageRoute
	generateReplyPath   = apiPrefix + generateReplyRoute
	streamReplyPath     = apiPrefix + streamReplyRoute
)

// RegisterRoutes connects the hand-written dependency-injected handler to the
// paths declared in idl/customer_service.thrift.
func RegisterRoutes(
	engine *route.Engine,
	handler *Handler,
	availability *Availability,
	security *SecurityMiddleware,
	accessLog *AccessLogMiddleware,
	rateLimit *RateLimitMiddleware,
) error {
	if engine == nil {
		return invalidDependency("route engine is nil")
	}
	if handler == nil {
		return invalidDependency("handler is nil")
	}
	if availability == nil {
		return invalidDependency("availability is nil")
	}
	if security == nil {
		return invalidDependency("security middleware is nil")
	}
	if accessLog == nil {
		return invalidDependency("access log middleware is nil")
	}
	if rateLimit == nil {
		return invalidDependency("rate limit middleware is nil")
	}

	engine.Use(recoverPanic, security.RequestID, accessLog.Log)
	engine.GET(healthPath, handler.Health)
	engine.GET(readinessPath, availability.Readiness)
	api := engine.Group(
		apiPrefix,
		availability.RequireReady,
		security.RequireBearerToken,
		rateLimit.Limit,
	)
	api.POST(receiveMessageRoute, handler.ReceiveMessage)
	api.POST(generateReplyRoute, handler.GenerateBotReply)
	api.POST(streamReplyRoute, handler.StreamBotReply)
	return nil
}
