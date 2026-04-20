BEGIN;

SET search_path TO private;

ALTER TABLE user_tokens ADD COLUMN IF NOT EXISTS refresh_token TEXT;

COMMIT;
