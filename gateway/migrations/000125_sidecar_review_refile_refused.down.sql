BEGIN;

SET search_path TO private;

-- Fails if a statement was refiled after a refusal: the old index allows one
-- non-EXECUTED row per statement.
DROP INDEX IF EXISTS index_reviews_sidecar_statement;
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_statement
    ON reviews (org_id, sidecar_id, listener_name, access_request_rule_name, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NOT NULL
      AND statement_hash IS NOT NULL
      AND status <> 'EXECUTED';

COMMIT;
