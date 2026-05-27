BEGIN;
SET search_path TO private;

CREATE TABLE IF NOT EXISTS events (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    event_type          TEXT        NOT NULL,
    payload             JSONB       NOT NULL,
    occurred_at         TIMESTAMPTZ NOT NULL,
    source              TEXT        NOT NULL DEFAULT '',
    producer_event_id   TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_events_org_type_occurred
    ON events (org_id, event_type, occurred_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_events_idempotency
    ON events (org_id, event_type, producer_event_id)
    WHERE producer_event_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS event_subscriptions (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    description         TEXT        NOT NULL DEFAULT '',
    event_types         TEXT[]      NOT NULL,
    runbook_repository  TEXT        NOT NULL,
    runbook_file        TEXT        NOT NULL,
    connection_name     VARCHAR(128) NOT NULL,
    parameter_mapping   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status              TEXT        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused')),
    created_by_user_id  TEXT        NOT NULL,
    created_by_email    TEXT        NOT NULL,
    created_by_groups   TEXT[]      NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name),
    FOREIGN KEY (org_id, connection_name) REFERENCES connections(org_id, name)
        ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_event_subscriptions_org_status ON event_subscriptions (org_id, status);
CREATE INDEX IF NOT EXISTS idx_event_subscriptions_event_types_gin ON event_subscriptions USING GIN (event_types);

CREATE TABLE IF NOT EXISTS event_dispatches (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            UUID        NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    event_id          UUID        NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    subscription_id   UUID        NOT NULL REFERENCES event_subscriptions(id) ON DELETE CASCADE,
    status            TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    attempt           INT         NOT NULL DEFAULT 0,
    session_id        UUID,
    last_error        TEXT,
    replayed_from     UUID        REFERENCES event_dispatches(id) ON DELETE SET NULL,
    dispatched_at     TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_event_dispatches_pending
    ON event_dispatches (created_at ASC)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_event_dispatches_sub_created
    ON event_dispatches (subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_dispatches_processing
    ON event_dispatches (status) WHERE status = 'processing';

COMMIT;
