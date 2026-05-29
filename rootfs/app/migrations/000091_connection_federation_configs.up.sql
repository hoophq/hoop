BEGIN;
SET search_path TO private;

CREATE TABLE IF NOT EXISTS connection_federation_configs (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  connection_id               UUID NOT NULL UNIQUE REFERENCES connections(id) ON DELETE CASCADE,
  hook_source                 TEXT NOT NULL CHECK (hook_source IN ('builtin')),
  builtin_provider            TEXT,
  admin_credentials_encrypted BYTEA,
  identity_source_attribute   TEXT NOT NULL DEFAULT '$.user.email',
  identity_target_template    TEXT NOT NULL DEFAULT '{user.email}',
  fallback_policy             TEXT NOT NULL DEFAULT 'deny' CHECK (fallback_policy IN ('deny', 'readonly')),
  readonly_principal          TEXT,
  token_ttl_seconds           INT NOT NULL DEFAULT 3600 CHECK (token_ttl_seconds > 0 AND token_ttl_seconds <= 43200),
  extra_config                JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at                  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at                  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT chk_builtin_provider_present
    CHECK (hook_source <> 'builtin' OR builtin_provider IS NOT NULL),
  CONSTRAINT chk_readonly_principal_present
    CHECK (fallback_policy <> 'readonly' OR readonly_principal IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_connection_federation_configs_org_id
  ON connection_federation_configs (org_id);

COMMIT;
