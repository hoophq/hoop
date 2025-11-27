BEGIN;

SET search_path TO private;

ALTER TABLE user_tokens DROP CONSTRAINT user_tokens_user_id_fkey;

COMMIT;
