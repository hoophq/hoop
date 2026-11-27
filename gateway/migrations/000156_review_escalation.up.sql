BEGIN;

SET search_path TO private;

-- When a pending sidecar review is unanswered past escalate_at, the escalation
-- job adds escalate_groups to private.review_groups, notifies their members by
-- direct message, and stamps escalated_at once.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS escalate_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS escalate_groups TEXT[] NULL,
    ADD COLUMN IF NOT EXISTS escalated_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_reviews_pending_escalation
    ON reviews (escalate_at)
    WHERE status = 'PENDING' AND escalate_at IS NOT NULL AND escalated_at IS NULL;

COMMIT;
