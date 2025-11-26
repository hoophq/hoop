BEGIN;

SET search_path TO private;

ALTER TABLE user_tokens
  ADD CONSTRAINT user_tokens_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(subject) ON DELETE CASCADE;

COMMIT;
