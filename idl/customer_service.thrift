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

service CustomerServiceAPI {
    HealthResponse Health(1: HealthRequest request) (api.get = "/healthz")

    ReceiveMessageResponse ReceiveMessage(1: ReceiveMessageRequest request) (
        api.post = "/v1/conversations/:conversation_id/messages"
    )

    GenerateBotReplyResponse GenerateBotReply(1: GenerateBotReplyRequest request) (
        api.post = "/v1/conversations/:conversation_id/messages/:trigger_message_id/reply"
    )
}
