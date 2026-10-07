BEGIN;

SET search_path TO private;

-- A row the requester or an admin added to record a denial, not a reviewer group.
ALTER TABLE review_groups ADD COLUMN IF NOT EXISTS added_on_denial BOOLEAN NOT NULL DEFAULT FALSE;

COMMIT;
