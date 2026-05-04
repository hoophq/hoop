BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS rdp_entity_detections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    frame_index INT  NOT NULL,
    timestamp   DOUBLE PRECISION NOT NULL,
    entity_type TEXT NOT NULL,
    score       DOUBLE PRECISION NOT NULL,
    x           INT NOT NULL,
    y           INT NOT NULL,
    width       INT NOT NULL,
    height      INT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rdp_entity_detections_session
    ON rdp_entity_detections (session_id);

COMMIT;
