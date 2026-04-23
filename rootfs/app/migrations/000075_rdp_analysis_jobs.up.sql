BEGIN;

SET search_path TO private;

CREATE TABLE rdp_analysis_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES orgs(id),
    session_id    UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INT  NOT NULL DEFAULT 0,
    attempt       INT  NOT NULL DEFAULT 0,
    last_error    TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT now(),
    started_at    TIMESTAMP,
    finished_at   TIMESTAMP
);

CREATE INDEX idx_rdp_analysis_jobs_status_priority
    ON rdp_analysis_jobs (status, priority DESC, created_at ASC)
    WHERE status IN ('pending', 'failed');

CREATE INDEX idx_rdp_analysis_jobs_session_id
    ON rdp_analysis_jobs (session_id);

COMMIT;
