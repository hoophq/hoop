BEGIN;

SET search_path TO private;

CREATE TABLE session_metrics (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs(id),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,

    info_type TEXT NOT NULL,
    count_masked INTEGER NOT NULL DEFAULT 0,
    count_analyzed INTEGER NOT NULL DEFAULT 0,

    connection_type TEXT NOT NULL,
    connection_subtype TEXT,

    session_created_at TIMESTAMP NOT NULL,
    session_ended_at TIMESTAMP,

    UNIQUE(session_id, info_type)
);

CREATE INDEX IF NOT EXISTS index_session_metrics_session_id ON session_metrics(session_id);

COMMIT;
