BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS sidecar_deleted_names;

-- A sidecar a service account created has no token, so it cannot exist once
-- key_hash is NOT NULL again.
DELETE FROM sidecars WHERE key_hash IS NULL;

ALTER TABLE sidecars
    DROP COLUMN IF EXISTS identity_issuer,
    DROP COLUMN IF EXISTS identity_subject,
    ALTER COLUMN key_hash SET NOT NULL;

DROP TABLE IF EXISTS sidecar_service_accounts;

COMMIT;
