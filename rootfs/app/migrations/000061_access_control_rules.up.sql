BEGIN;

SET search_path TO private;

CREATE TABLE access_control_rules(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs(id),

    name TEXT NOT NULL,
    description TEXT,

    reviewers_groups TEXT[] NOT NULL,
    force_approve_groups TEXT[] DEFAULT '{}'::TEXT[],
    access_max_duration INT,
    min_approvals INT,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_access_control_rules_org_name ON access_control_rules(org_id, name);

COMMIT;
