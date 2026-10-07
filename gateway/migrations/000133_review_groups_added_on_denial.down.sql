BEGIN;

SET search_path TO private;

ALTER TABLE review_groups DROP COLUMN IF EXISTS added_on_denial;

COMMIT;
