BEGIN;

SET search_path TO private;

CREATE TABLE ai_providers (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    feature VARCHAR(255) NOT NULL,
    provider VARCHAR(255) NOT NULL,
    api_url TEXT,
    api_key TEXT,
    model VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_ai_session_analyzer_rules_org_feature 
ON private.ai_providers (org_id, feature);

CREATE TABLE ai_session_analyzer_rules (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    name VARCHAR(255) NOT NULL,
    description TEXT,

    connection_names TEXT[] NOT NULL,
    risk_evaluation JSONB NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_ai_session_analyzer_rules_org_name ON ai_session_analyzer_rules (org_id, name);

ALTER TABLE sessions ADD ai_analysis jsonb NULL;

COMMIT;
