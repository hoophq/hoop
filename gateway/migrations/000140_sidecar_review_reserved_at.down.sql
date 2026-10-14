BEGIN;

SET search_path TO private;

-- The older binary cannot settle a reservation. Spend the open ones, so no
-- review stays PROCESSING with nothing to move it.
UPDATE sessions AS s SET status = 'done', ended_at = (now() AT TIME ZONE 'UTC')
FROM reviews AS r
WHERE r.org_id = s.org_id AND r.session_id = s.id
  AND r.status = 'PROCESSING' AND r.reserved_at IS NOT NULL;

UPDATE reviews SET status = 'EXECUTED'
WHERE status = 'PROCESSING' AND reserved_at IS NOT NULL;

ALTER TABLE reviews DROP COLUMN IF EXISTS reserved_at;

COMMIT;
