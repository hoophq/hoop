BEGIN;

SET search_path TO private;

ALTER TABLE datamasking_rules ADD COLUMN IF NOT EXISTS strategy VARCHAR(50) NOT NULL DEFAULT 'redact';
ALTER TABLE datamasking_rules ADD COLUMN IF NOT EXISTS columns TEXT[] NULL;
ALTER TABLE datamasking_rules ADD COLUMN IF NOT EXISTS keep_last INT NULL;
ALTER TABLE datamasking_rules ADD COLUMN IF NOT EXISTS mask_char VARCHAR(10) NULL;

COMMIT;
