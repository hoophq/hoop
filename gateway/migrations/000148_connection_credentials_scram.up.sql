BEGIN;

SET search_path TO private;

-- SCRAM-SHA-256 verifier of a sidecar lane credential, computed at mint.
-- NULL on every other credential.
ALTER TABLE connection_credentials ADD COLUMN IF NOT EXISTS scram_sha256 JSONB NULL;

COMMIT;
