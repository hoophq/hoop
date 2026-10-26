BEGIN;

SET search_path TO private;

-- No sidecar_spec is rewritten. A spec the new form wrote stays as written,
-- and the older binary refuses to serve it.

-- The older binary would release an approved agent decision on a claim. The
-- sidecar forwarded it when it filed it, so it is spent.
UPDATE reviews SET status = 'EXECUTED'
WHERE approver_type = 'ai_agent' AND status = 'APPROVED';

DROP INDEX IF EXISTS index_reviews_sidecar_level_statement;

ALTER TABLE reviews
    DROP COLUMN IF EXISTS risk_level,
    DROP COLUMN IF EXISTS decision_reason,
    DROP COLUMN IF EXISTS approver_type;

COMMIT;
