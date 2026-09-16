namespace go model

struct HealthRequest {}

struct HealthResponse {
    1: required string Status (api.body = "status")
}

struct ReceiveMessageRequest {
    1: required string ConversationID (
        api.path = "conversation_id",
        api.vd = "$!=''"
    )
    2: required string CustomerID (
        api.body = "customer_id",
        api.vd = "$!=''"
    )
    3: required string Channel (
        api.body = "channel",
        api.vd = "$!=''"
    )
    4: required string ExternalMessageID (
        api.body = "external_message_id",
        api.vd = "$!=''"
    )
    5: required string Content (
        api.body = "content",
        api.vd = "$!=''"
    )
}

struct ReceiveMessageResponse {
    1: required string ConversationID (api.body = "conversation_id")
    2: required string MessageID (api.body = "message_id")
    3: required string Status (api.body = "status")
    4: required string NextAction (api.body = "next_action")
    5: required bool Duplicate (api.body = "duplicate")
}

struct GenerateBotReplyRequest {
    1: required string ConversationID (
        api.path = "conversation_id",
        api.vd = "$!=''"
    )
    2: required string TriggerMessageID (
        api.path = "trigger_message_id",
        api.vd = "$!=''"
    )
}

struct GenerateBotReplyResponse {
    1: required string ConversationID (api.body = "conversation_id")
    2: required string TriggerMessageID (api.body = "trigger_message_id")
    3: required string MessageID (api.body = "message_id")
    4: required string Content (api.body = "content")
    5: required bool Duplicate (api.body = "duplicate")
}

// The stream endpoint uses these DTOs as SSE data payloads. The HTTP response
// itself is text/event-stream, so StreamBotReplyResponse is only the service
// method's schema placeholder and is not serialized by the Hertz handler.
struct ReplyDeltaEventData {
    1: required string Delta (api.body = "delta")
}

struct ReplyCompletedEventData {
    1: required string ConversationID (api.body = "conversation_id")
    2: required string TriggerMessageID (api.body = "trigger_message_id")
    3: required string MessageID (api.body = "message_id")
    4: required string Content (api.body = "content")
    5: required bool Duplicate (api.body = "duplicate")
}

struct ReplySupersededEventData {
    1: required string ConversationID (api.body = "conversation_id")
    2: required string TriggerMessageID (api.body = "trigger_message_id")
    3: required string Message (api.body = "message")
}

struct StreamBotReplyResponse {}

// Snapshot only: not a lease, delivery receipt or authorization token.
struct DeliveryEligibilityRequest {
 1: required string ConversationID (api.path = "conversation_id", api.vd = "$!=''")
 2: required string TriggerMessageID (api.path = "trigger_message_id", api.vd = "$!=''")
 3: required string CustomerID (api.body = "customer_id", api.vd = "$!=''")
 4: required string Channel (api.body = "channel", api.vd = "$!=''")
 5: required string ReplyMessageID (api.body = "reply_message_id", api.vd = "$!=''")
 6: required string Content (api.body = "content", api.vd = "$!=''")
}
struct DeliveryEligibilityResponse {
 1: required bool Eligible (api.body = "eligible")
 2: required string Reason (api.body = "reason")
}

service CustomerServiceAPI {
    DeliveryEligibilityResponse CheckDeliveryEligibility(1: DeliveryEligibilityRequest request) (
        api.post = "/v1/conversations/:conversation_id/messages/:trigger_message_id/reply/eligibility"
    )
    HealthResponse Health(1: HealthRequest request) (api.get = "/healthz")

    // Liveness and readiness deliberately reuse the same small response DTO;
    // their status codes and lifecycle semantics are different.
    HealthResponse Ready(1: HealthRequest request) (api.get = "/readyz")

    ReceiveMessageResponse ReceiveMessage(1: ReceiveMessageRequest request) (
        api.post = "/v1/conversations/:conversation_id/messages"
    )

    GenerateBotReplyResponse GenerateBotReply(1: GenerateBotReplyRequest request) (
        api.post = "/v1/conversations/:conversation_id/messages/:trigger_message_id/reply"
    )

    StreamBotReplyResponse StreamBotReply(1: GenerateBotReplyRequest request) (
        api.post = "/v1/conversations/:conversation_id/messages/:trigger_message_id/reply/stream"
    )
}
