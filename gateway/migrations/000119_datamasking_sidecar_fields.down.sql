BEGIN;

SET search_path TO private;

ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS mask_char;
ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS keep_last;
ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS columns;
ALTER TABLE datamasking_rules DROP COLUMN IF EXISTS strategy;

COMMIT;
