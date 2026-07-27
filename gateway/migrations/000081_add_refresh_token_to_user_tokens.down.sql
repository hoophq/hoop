BEGIN;

SET search_path TO private;

ALTER TABLE user_tokens DROP COLUMN IF EXISTS refresh_token;

COMMIT;
