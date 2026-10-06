BEGIN;

SET search_path TO private;

-- EXPIRED: a sidecar review past its deadline. It is terminal. Only the control
-- plane writes it, always with statement_hash NULL, so the 000125 index skips it.
ALTER TYPE enum_reviews_status ADD VALUE IF NOT EXISTS 'EXPIRED';

ALTER TABLE access_request_rules
    ADD COLUMN IF NOT EXISTS pending_ttl_sec INT NULL,
    ADD COLUMN IF NOT EXISTS approval_ttl_sec INT NULL;

ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP NULL,
    ADD COLUMN IF NOT EXISTS approval_ttl_sec INT NULL;

-- A sidecar review's deadline can pass the 48h Slack retention. The rows
-- outlive it, so an expiry still finds the messages to rewrite.
ALTER TABLE slack_review_messages ADD COLUMN IF NOT EXISTS deadline TIMESTAMPTZ NULL;
ALTER TABLE slack_review_settlements ADD COLUMN IF NOT EXISTS deadline TIMESTAMPTZ NULL;

COMMIT;
