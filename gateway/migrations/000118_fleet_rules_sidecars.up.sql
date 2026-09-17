BEGIN;

SET search_path TO private;

-- 1. Guardrail Rules to Sidecars Mapping
CREATE TABLE IF NOT EXISTS guardrail_rules_sidecars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES guardrail_rules(id) ON DELETE CASCADE,
    sidecar_id UUID NOT NULL REFERENCES sidecars(id) ON DELETE CASCADE,
    listener_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, rule_id, sidecar_id, listener_name)
);

CREATE INDEX IF NOT EXISTS idx_gr_rules_sidecars ON guardrail_rules_sidecars(sidecar_id, listener_name);

-- 2. Data Masking Rules to Sidecars Mapping
CREATE TABLE IF NOT EXISTS datamasking_rules_sidecars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    rule_id UUID NOT NULL REFERENCES datamasking_rules(id) ON DELETE CASCADE,
    sidecar_id UUID NOT NULL REFERENCES sidecars(id) ON DELETE CASCADE,
    listener_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, rule_id, sidecar_id, listener_name)
);

CREATE INDEX IF NOT EXISTS idx_dm_rules_sidecars ON datamasking_rules_sidecars(sidecar_id, listener_name);

-- 3. AI Session Analyzer Rules to Sidecars Mapping
CREATE TABLE IF NOT EXISTS ai_session_analyzer_rules_sidecars (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    analyzer_rule_id UUID NOT NULL REFERENCES ai_session_analyzer_rules(id) ON DELETE CASCADE,
    sidecar_id UUID NOT NULL REFERENCES sidecars(id) ON DELETE CASCADE,
    listener_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, analyzer_rule_id, sidecar_id, listener_name)
);

CREATE INDEX IF NOT EXISTS idx_ai_rules_sidecars ON ai_session_analyzer_rules_sidecars(sidecar_id, listener_name);

COMMIT;
