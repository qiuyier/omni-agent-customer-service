-- This schema is owned exclusively by this service. Deliberately omit
-- IF NOT EXISTS so a pre-existing, foreign-owned schema fails safely instead
-- of later being removed by this migration's down section.
CREATE SCHEMA customer_service;

CREATE TABLE customer_service.conversations (
    id text PRIMARY KEY,
    customer_id text NOT NULL,
    channel text NOT NULL,
    status text NOT NULL,
    agent_id text,
    revision bigint NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,

    CONSTRAINT conversations_id_channel_key UNIQUE (id, channel),
    CONSTRAINT conversations_id_not_blank CHECK (btrim(id) <> ''),
    CONSTRAINT conversations_customer_id_not_blank CHECK (btrim(customer_id) <> ''),
    CONSTRAINT conversations_channel_not_blank CHECK (btrim(channel) <> ''),
    CONSTRAINT conversations_status_valid CHECK (
        status IN ('bot_active', 'waiting_human', 'human_active', 'closed')
    ),
    CONSTRAINT conversations_agent_id_not_blank CHECK (
        agent_id IS NULL OR btrim(agent_id) <> ''
    ),
    CONSTRAINT conversations_agent_assignment_valid CHECK (
        (status = 'human_active' AND agent_id IS NOT NULL)
        OR (status IN ('bot_active', 'waiting_human') AND agent_id IS NULL)
        OR status = 'closed'
    ),
    CONSTRAINT conversations_revision_positive CHECK (revision > 0),
    CONSTRAINT conversations_timestamps_ordered CHECK (updated_at >= created_at)
);

CREATE TABLE customer_service.messages (
    sequence_no bigint GENERATED ALWAYS AS IDENTITY,
    id text PRIMARY KEY,
    conversation_id text NOT NULL,
    channel text NOT NULL,
    external_message_id text,
    reply_to_message_id text,
    sender text NOT NULL,
    sender_id text,
    content text NOT NULL,
    created_at timestamptz NOT NULL,

    CONSTRAINT messages_sequence_no_key UNIQUE (sequence_no),
    CONSTRAINT messages_id_conversation_key UNIQUE (id, conversation_id),
    CONSTRAINT messages_conversation_channel_fkey FOREIGN KEY (conversation_id, channel)
        REFERENCES customer_service.conversations (id, channel) ON DELETE RESTRICT,
    CONSTRAINT messages_reply_target_fkey FOREIGN KEY (reply_to_message_id, conversation_id)
        REFERENCES customer_service.messages (id, conversation_id) ON DELETE RESTRICT,
    CONSTRAINT messages_id_not_blank CHECK (btrim(id) <> ''),
    CONSTRAINT messages_conversation_id_not_blank CHECK (btrim(conversation_id) <> ''),
    CONSTRAINT messages_channel_not_blank CHECK (btrim(channel) <> ''),
    CONSTRAINT messages_external_id_not_blank CHECK (
        external_message_id IS NULL OR btrim(external_message_id) <> ''
    ),
    CONSTRAINT messages_reply_to_not_blank CHECK (
        reply_to_message_id IS NULL OR btrim(reply_to_message_id) <> ''
    ),
    CONSTRAINT messages_sender_valid CHECK (
        sender IN ('customer', 'bot', 'human_agent', 'system', 'tool')
    ),
    CONSTRAINT messages_sender_id_not_blank CHECK (
        sender_id IS NULL OR btrim(sender_id) <> ''
    ),
    CONSTRAINT messages_actor_id_required CHECK (
        sender NOT IN ('customer', 'human_agent') OR sender_id IS NOT NULL
    ),
    CONSTRAINT messages_reply_sender_valid CHECK (
        reply_to_message_id IS NULL
        OR (sender = 'bot' AND reply_to_message_id <> id)
    ),
    CONSTRAINT messages_content_not_blank CHECK (btrim(content) <> '')
);

CREATE UNIQUE INDEX messages_channel_external_message_id_unique
    ON customer_service.messages (channel, external_message_id)
    WHERE external_message_id IS NOT NULL;

CREATE UNIQUE INDEX messages_reply_to_message_id_unique
    ON customer_service.messages (reply_to_message_id)
    WHERE reply_to_message_id IS NOT NULL;

CREATE INDEX messages_conversation_sequence_idx
    ON customer_service.messages (conversation_id, sequence_no);

---- create above / drop below ----

DROP SCHEMA IF EXISTS customer_service CASCADE;
