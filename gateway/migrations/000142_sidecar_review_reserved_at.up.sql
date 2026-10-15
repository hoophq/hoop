BEGIN;

SET search_path TO private;

-- reserved_at: when a sidecar took an approval it settles after the upstream
-- answers (spend_approval_on: accepted). PROCESSING while it is set; the 000125
-- index keeps the row live, so a resend of the same bytes matches it.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS reserved_at TIMESTAMP NULL;

COMMIT;
