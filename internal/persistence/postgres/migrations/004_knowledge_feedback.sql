-- No customer/session identity or unredacted query is stored here.
CREATE TABLE customer_service.knowledge_feedback (
 id text PRIMARY KEY CHECK (length(id)=36),
 question text NOT NULL UNIQUE CHECK (char_length(question) BETWEEN 1 AND 512),
 count integer NOT NULL DEFAULT 1 CHECK (count BETWEEN 1 AND 1000000),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ignored','resolved')),
 faq_id text NOT NULL DEFAULT '' CHECK (char_length(faq_id)<=128),
 first_seen timestamptz NOT NULL DEFAULT now(),
 last_seen timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT (now()+interval '7 days'),
 CHECK (expires_at=first_seen+interval '7 days')
);
CREATE INDEX knowledge_feedback_expiry ON customer_service.knowledge_feedback(expires_at);

---- create above / drop below ----
DROP TABLE customer_service.knowledge_feedback;
