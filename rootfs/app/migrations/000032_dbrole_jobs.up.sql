BEGIN;

SET search_path TO private;

CREATE TABLE dbrole_jobs(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    spec JSONB NULL,
    status VARCHAR(128) NOT NULL,
    error_message TEXT NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

ALTER TABLE private.env_vars ADD COLUMN updated_at TIMESTAMP NULL;


COMMIT;
