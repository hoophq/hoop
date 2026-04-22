BEGIN;
SET search_path TO private;
ALTER TABLE sessions DROP COLUMN IF EXISTS identity_type;
ALTER TABLE sessions DROP COLUMN IF EXISTS machine_identity_id;
DROP TABLE IF EXISTS machine_identities_attributes;
DROP TABLE IF EXISTS machine_identity_credentials;
DROP TABLE IF EXISTS machine_identities;
ALTER TABLE connection_credentials DROP COLUMN IF EXISTS secret_key;
COMMIT;
