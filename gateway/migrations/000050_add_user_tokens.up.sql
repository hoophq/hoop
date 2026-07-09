BEGIN;

SET search_path TO private;

CREATE TABLE user_tokens (
  user_id TEXT PRIMARY KEY REFERENCES users(subject) ON DELETE CASCADE,
  token TEXT NOT NULL
);

COMMIT;
