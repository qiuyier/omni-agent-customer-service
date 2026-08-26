package postgres

const conversationColumns = `
    id,
    customer_id,
    channel,
    status,
    agent_id,
    revision,
    created_at,
    updated_at`

const messageColumns = `
    id,
    conversation_id,
    external_message_id,
    reply_to_message_id,
    sender,
    sender_id,
    content,
    created_at`

const findConversationSQL = `
SELECT ` + conversationColumns + `
FROM customer_service.conversations
WHERE id = $1`

const findConversationForUpdateSQL = findConversationSQL + `
FOR UPDATE`

const findMessageByExternalIDSQL = `
SELECT ` + messageColumns + `
FROM customer_service.messages
WHERE channel = $1 AND external_message_id = $2`

const findReplyByTriggerMessageIDSQL = `
SELECT ` + messageColumns + `
FROM customer_service.messages
WHERE reply_to_message_id = $1`

const findMessageByIDSQL = `
SELECT ` + messageColumns + `
FROM customer_service.messages
WHERE id = $1`

const listMessagesSQL = `
SELECT ` + messageColumns + `
FROM customer_service.messages
WHERE conversation_id = $1
ORDER BY sequence_no`

const insertConversationSQL = `
INSERT INTO customer_service.conversations (
    id,
    customer_id,
    channel,
    status,
    agent_id,
    revision,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING`

const updateConversationSQL = `
UPDATE customer_service.conversations
SET status = $2,
    agent_id = $3,
    revision = $4,
    updated_at = $5
WHERE id = $1`

const insertMessageSQL = `
INSERT INTO customer_service.messages (
    id,
    conversation_id,
    channel,
    external_message_id,
    reply_to_message_id,
    sender,
    sender_id,
    content,
    created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT DO NOTHING`

const advisoryLockSQL = `SELECT pg_advisory_xact_lock($1)`
