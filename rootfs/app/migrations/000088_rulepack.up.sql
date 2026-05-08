BEGIN;

SET search_path TO private;

CREATE TABLE IF NOT EXISTS rulepacks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    display_name VARCHAR(255) NOT NULL,
    description  TEXT,
    version      TEXT,
    tags         TEXT[] NOT NULL DEFAULT '{}',
    is_managed   BOOLEAN NOT NULL DEFAULT false,
    is_paid      BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_rulepacks_org_display_name UNIQUE (org_id, display_name)
);

ALTER TABLE attributes
    ADD COLUMN rulepack_id UUID NULL REFERENCES rulepacks(id) ON DELETE CASCADE;

CREATE INDEX idx_attributes_rulepack_id ON attributes(rulepack_id);

COMMIT;
