BEGIN;

SET search_path TO private;

ALTER TABLE users ADD COLUMN password TEXT;

CREATE TABLE local_auth_sessions (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id),
    token TEXT,
    expires_at TIMESTAMP NOT NULL
);

COMMIT;
