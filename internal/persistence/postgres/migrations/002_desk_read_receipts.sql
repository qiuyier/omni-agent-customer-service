CREATE TABLE customer_service.desk_read_receipts (
    agent_id text NOT NULL CHECK (btrim(agent_id) <> ''),
    message_id text NOT NULL REFERENCES customer_service.messages(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, message_id)
);

---- create above / drop below ----
DROP TABLE customer_service.desk_read_receipts;
