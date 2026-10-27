BEGIN;

SET search_path TO private;

ALTER TABLE connection_credentials DROP COLUMN IF EXISTS scram_sha256;

COMMIT;
