BEGIN;

SET search_path TO private;

-- Stable-key support: store an encrypted copy of the plaintext secret key so
-- repeated calls to CreateConnectionCredentials for the same (user, connection)
-- pair can return the same token. The existing secret_key_hash column stays and
-- remains the lookup index for proxy auth.
ALTER TABLE connection_credentials
    ADD COLUMN IF NOT EXISTS encrypted_secret_key BYTEA NULL,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMP NULL;

-- Partial index used to locate the active (non-revoked) credential for a
-- (org, user, connection) triple. Revoked rows are kept for forensics.
CREATE INDEX IF NOT EXISTS idx_conn_cred_active_user_conn
    ON connection_credentials (org_id, user_subject, connection_name)
    WHERE revoked_at IS NULL;

-- Server-wide symmetric key used to encrypt/decrypt the stored plaintext secret
-- keys. Auto-generated on first startup by the gateway, following the same
-- pattern as shared_signing_key.
ALTER TABLE serverconfig
    ADD COLUMN IF NOT EXISTS credential_encryption_key TEXT NULL;

COMMIT;
