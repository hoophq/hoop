BEGIN;

SET search_path TO private;

-- Set when an active user signs in or refreshes an access token. No default,
-- so users created before this migration stay NULL instead of the upgrade time.
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ NULL;

COMMIT;
