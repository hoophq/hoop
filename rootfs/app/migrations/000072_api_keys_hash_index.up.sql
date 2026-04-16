BEGIN;

CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON private.api_keys(key_hash);

COMMIT;
