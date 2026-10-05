BEGIN;

SET search_path TO private;

-- Where each review message landed in Slack, so any control plane replica can
-- rewrite it: the one that handles the click or the API call is rarely the one
-- that posted. blocks is the block set as posted, the base every rewrite
-- starts from. Gateway mode keeps this in memory and never writes here.
CREATE TABLE IF NOT EXISTS slack_review_messages (
    review_id  UUID NOT NULL,
    org_id     UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    channel_id VARCHAR(64) NOT NULL,
    ts         VARCHAR(64) NOT NULL,
    event_kind VARCHAR(32) NOT NULL,
    blocks     JSONB NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (review_id, channel_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_slack_review_messages_sent_at ON slack_review_messages (sent_at);

-- The terminal rewrite a review got. A message posted after it is rewritten
-- with it, and a revoke rewrites what an approval did (rewritable).
CREATE TABLE IF NOT EXISTS slack_review_settlements (
    review_id  UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    request    JSONB NOT NULL,
    rewritable BOOLEAN NOT NULL DEFAULT FALSE,
    settled_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_slack_review_settlements_settled_at ON slack_review_settlements (settled_at);

COMMIT;
