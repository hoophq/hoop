BEGIN;

SET search_path TO private;

CREATE TABLE access_request_rules(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs(id),

    name TEXT NOT NULL,
    description TEXT,

    connection_names TEXT[] NOT NULL,
    access_type VARCHAR(16) NOT NULL,

    approval_required_groups TEXT[] NOT NULL,
    all_groups_must_approve BOOLEAN DEFAULT FALSE,

    reviewers_groups TEXT[] NOT NULL,
    force_approval_groups TEXT[] NOT NULL,

    access_max_duration INT,
    min_approvals INT,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_access_request_rules_org_name ON access_request_rules(org_id, name);
CREATE INDEX idx_access_request_rules_org_id_access_type ON access_request_rules(org_id, access_type);

COMMIT;
