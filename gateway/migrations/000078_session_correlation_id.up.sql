BEGIN;

SET search_path TO private;

ALTER TABLE sessions
ADD COLUMN correlation_id VARCHAR(255) NULL;

CREATE INDEX IF NOT EXISTS index_sessions_correlation_id
ON sessions(org_id, correlation_id)
WHERE correlation_id IS NOT NULL;

COMMIT;