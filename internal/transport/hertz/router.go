package hertz

import "github.com/cloudwego/hertz/pkg/route"

const (
	healthPath         = "/healthz"
	receiveMessagePath = "/v1/conversations/:conversation_id/messages"
	generateReplyPath  = "/v1/conversations/:conversation_id/messages/:trigger_message_id/reply"
)

// RegisterRoutes connects the hand-written dependency-injected handler to the
// paths declared in idl/customer_service.thrift.
func RegisterRoutes(engine *route.Engine, handler *Handler) error {
	if engine == nil {
		return invalidDependency("route engine is nil")
	}
	if handler == nil {
		return invalidDependency("handler is nil")
	}

	engine.GET(healthPath, handler.Health)
	engine.POST(receiveMessagePath, handler.ReceiveMessage)
	engine.POST(generateReplyPath, handler.GenerateBotReply)
	return nil
}
