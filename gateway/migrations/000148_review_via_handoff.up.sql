BEGIN;

SET search_path TO private;

-- A review filed for a hold the hand-off agent of an ai-handoff level
-- selected. Existing rows were filed directly.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS via_handoff BOOLEAN NOT NULL DEFAULT false;

-- A hand-off review and a direct review of the same level and statement are
-- two filings: neither matches nor blocks the other.
DROP INDEX IF EXISTS index_reviews_sidecar_level_statement;
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_level_statement
    ON reviews (org_id, sidecar_id, listener_name, risk_level, via_handoff, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NULL
      AND risk_level IS NOT NULL
      AND statement_hash IS NOT NULL
      AND approver_type = 'human'
      AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED');

COMMIT;
