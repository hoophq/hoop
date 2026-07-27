BEGIN;

SET search_path TO private;

CREATE TABLE audit(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    event VARCHAR(255) NOT NULL,
    metadata JSONB NULL,

    created_by VARCHAR(255) NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE VIEW public.audit AS
    SELECT id, org_id, event, metadata, created_by, created_at FROM audit;

COMMIT;