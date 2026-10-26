BEGIN;

SET search_path TO private;

-- The older index keys a level review without via_handoff. A live hand-off
-- review beside a live direct review of the same statement would break it,
-- so the hand-off one expires, as a lapsed review does.
WITH expired AS (
    UPDATE reviews AS h SET status = 'EXPIRED', statement_hash = NULL
    WHERE h.via_handoff
      AND h.sidecar_id IS NOT NULL AND h.listener_name IS NOT NULL
      AND h.access_request_rule_name IS NULL AND h.risk_level IS NOT NULL
      AND h.statement_hash IS NOT NULL AND h.approver_type = 'human'
      AND h.status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED')
      AND EXISTS (
        SELECT 1 FROM reviews AS d
        WHERE NOT d.via_handoff
          AND d.org_id = h.org_id AND d.sidecar_id = h.sidecar_id
          AND d.listener_name = h.listener_name AND d.risk_level = h.risk_level
          AND d.statement_hash = h.statement_hash
          AND d.access_request_rule_name IS NULL AND d.approver_type = 'human'
          AND d.status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED'))
    RETURNING h.org_id, h.session_id
)
UPDATE sessions AS s SET status = 'done'
FROM expired AS e
WHERE s.org_id = e.org_id AND s.id = e.session_id AND s.status <> 'done';

DROP INDEX IF EXISTS index_reviews_sidecar_level_statement;
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_level_statement
    ON reviews (org_id, sidecar_id, listener_name, risk_level, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NULL
      AND risk_level IS NOT NULL
      AND statement_hash IS NOT NULL
      AND approver_type = 'human'
      AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED');

ALTER TABLE reviews DROP COLUMN IF EXISTS via_handoff;

COMMIT;
