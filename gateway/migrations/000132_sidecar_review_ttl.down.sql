BEGIN;

SET search_path TO private;

-- The older binary has no deadline. Settle lapsed live sidecar reviews, so it
-- cannot release them.
UPDATE reviews SET status = 'EXPIRED', statement_hash = NULL
WHERE listener_name IS NOT NULL
  AND status IN ('PENDING', 'APPROVED')
  AND expires_at IS NOT NULL
  AND expires_at <= (now() AT TIME ZONE 'UTC');

UPDATE reviews SET statement_hash = NULL
WHERE status = 'EXPIRED' AND statement_hash IS NOT NULL;

UPDATE sessions AS s SET status = 'done'
FROM reviews AS r
WHERE r.org_id = s.org_id AND r.session_id = s.id
  AND r.status = 'EXPIRED' AND s.status <> 'done';

ALTER TABLE reviews
    DROP COLUMN IF EXISTS approval_ttl_sec,
    DROP COLUMN IF EXISTS expires_at;

ALTER TABLE slack_review_messages DROP COLUMN IF EXISTS deadline;
ALTER TABLE slack_review_settlements DROP COLUMN IF EXISTS deadline;

ALTER TABLE access_request_rules
    DROP COLUMN IF EXISTS approval_ttl_sec,
    DROP COLUMN IF EXISTS pending_ttl_sec;

-- The EXPIRED label stays: Postgres cannot drop an enum value.

COMMIT;
