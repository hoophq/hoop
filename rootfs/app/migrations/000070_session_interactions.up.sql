CREATE TYPE enum_session_type AS ENUM ('human', 'machine');
ALTER TABLE private.sessions ADD COLUMN type enum_session_type NOT NULL DEFAULT 'human';

CREATE TABLE private.session_interactions (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES private.sessions(id),
    org_id UUID NOT NULL REFERENCES private.orgs(id),
    sequence INTEGER NOT NULL,
    blob_input_id UUID NULL,
    blob_stream_id UUID NULL,
    exit_code SMALLINT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    ended_at TIMESTAMP NULL,
    UNIQUE(session_id, sequence)
);
CREATE INDEX idx_session_interactions_session_created ON private.session_interactions (session_id, created_at);