BEGIN;

SET search_path TO private;

-- Set by every token upsert: each sign-in and each OIDC refresh. No default,
-- so rows stored before this migration stay NULL instead of the upgrade time.
ALTER TABLE user_tokens ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ NULL;

COMMIT;
