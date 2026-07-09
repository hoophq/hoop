BEGIN;

SET search_path TO private;

ALTER TABLE resources DROP COLUMN type;
ALTER TABLE resources RENAME COLUMN subtype TO "type";

COMMIT;
