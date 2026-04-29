BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_conn_cred_active_user_conn;

ALTER TABLE connection_credentials
    DROP COLUMN IF EXISTS encrypted_secret_key,
    DROP COLUMN IF EXISTS revoked_at;

ALTER TABLE serverconfig
    DROP COLUMN IF EXISTS credential_encryption_key;

COMMIT;
