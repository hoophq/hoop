BEGIN;

SET search_path TO private;

-- The one-time code a user sends to the Hoop bot in Slack to link a Slack
-- user. One live code per user: a new one replaces it. Only the hash is
-- stored. In the database, because the replica that holds the Slack socket is
-- rarely the one that made the code.
CREATE TABLE IF NOT EXISTS slack_link_codes (
    org_id     UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  VARCHAR(64) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, user_id),
    UNIQUE (org_id, code_hash)
);

COMMIT;
