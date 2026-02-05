BEGIN;

SET search_path TO private;

CREATE TABLE access_request_rules(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs(id),

    name TEXT NOT NULL,
    description TEXT,

    reviewers_groups TEXT[] NOT NULL,
    force_approval_groups TEXT[] DEFAULT '{}'::TEXT[],
    access_max_duration INT,
    min_approvals INT,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_access_request_rules_org_name ON access_request_rules(org_id, name);

ALTER TABLE connections ADD COLUMN IF NOT EXISTS access_request_rule_name VARCHAR(128),
    ADD CONSTRAINT connection_access_request_rule_fk
        FOREIGN KEY (org_id, access_request_rule_name)
        REFERENCES access_request_rules(org_id, name)
        ON UPDATE CASCADE ON DELETE SET NULL;

COMMIT;
