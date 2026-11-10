BEGIN;

SET search_path TO private;

-- The analyzer risk level a sidecar filed the review under, in place of an
-- approval rule. Null on a review filed under a rule.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS risk_level TEXT NULL
        CONSTRAINT reviews_risk_level_check CHECK (risk_level IN ('high', 'medium', 'low'));

-- One live review per sidecar, listener, risk level and statement. The 000125
-- index skips these rows: their access_request_rule_name is NULL.
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_level_statement
    ON reviews (org_id, sidecar_id, listener_name, risk_level, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NULL
      AND risk_level IS NOT NULL
      AND statement_hash IS NOT NULL
      AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED');

COMMIT;
