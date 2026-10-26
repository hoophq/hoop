BEGIN;

SET search_path TO private;

-- Who decides a review: human, or ai_agent for a review a sidecar's approval
-- agent filed already decided. Existing rows read as human.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS approver_type TEXT NOT NULL DEFAULT 'human'
        CONSTRAINT reviews_approver_type_check CHECK (approver_type IN ('human', 'ai_agent')),
    ADD COLUMN IF NOT EXISTS decision_reason TEXT NULL,
    ADD COLUMN IF NOT EXISTS risk_level TEXT NULL
        CONSTRAINT reviews_risk_level_check CHECK (risk_level IN ('high', 'medium', 'low'));

-- One live review per sidecar, listener, risk level and statement, for a
-- review filed under a risk level instead of an approval rule. The 000125
-- index skips these rows: their access_request_rule_name is NULL.
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_level_statement
    ON reviews (org_id, sidecar_id, listener_name, risk_level, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NULL
      AND risk_level IS NOT NULL
      AND statement_hash IS NOT NULL
      AND approver_type = 'human'
      AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED');

-- No sidecar_spec is rewritten. An older gateway replica decodes the spec
-- strictly, and a hold in the bare form under the rule's own approval rule
-- keeps working here; the next save writes the object form.

COMMIT;
