BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_reviews_pending_escalation;

ALTER TABLE reviews
    DROP COLUMN IF EXISTS escalated_at,
    DROP COLUMN IF EXISTS escalate_groups,
    DROP COLUMN IF EXISTS escalate_at;

COMMIT;
