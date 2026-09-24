BEGIN;

SET search_path TO private;

-- Where a sidecar's reviews are posted in Slack (ADR-0019). listener_name ''
-- means the whole sidecar; a listener's own row replaces it. With neither, the
-- org's default channel applies, and that channel receives every review
-- anyway, as it does on the gateway. listener_name is not a foreign key: a
-- listener is an element of sidecars.configuration, and a configuration write
-- drops the rows of listeners it removed. An empty list is no row, not a row
-- that means none.
CREATE TABLE IF NOT EXISTS sidecar_slack_channels (
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    sidecar_id    UUID NOT NULL REFERENCES sidecars(id) ON DELETE CASCADE,
    listener_name VARCHAR(255) NOT NULL DEFAULT '',
    channels      TEXT[] NOT NULL CHECK (cardinality(channels) > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, sidecar_id, listener_name)
);

COMMIT;
