-- One instance-wide keyword FAQ store; not a tenant-routing boundary.
-- State, versions and audit are committed by one conditional row replacement.
CREATE TABLE customer_service.knowledge_management (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision bigint NOT NULL CHECK (revision > 0 AND revision <= 1000),
    snapshot jsonb NOT NULL CHECK (
        jsonb_typeof(snapshot) = 'object'
        AND octet_length(snapshot::text) <= 50331648
        AND snapshot ? 'revision'
        AND (snapshot->>'revision')::bigint = revision
    )
);

---- create above / drop below ----
-- Destructive: rollback removes all managed FAQ drafts, versions and audit.
DROP TABLE customer_service.knowledge_management;
