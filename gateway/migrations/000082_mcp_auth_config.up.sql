BEGIN;

SET search_path TO private;

-- Per-org MCP OAuth 2.1 Resource Server configuration. Stored as JSONB to keep
-- the schema flexible while the spec evolves and to follow the same pattern
-- used by oidc_config and saml_config on the same table.
--
-- Expected shape:
-- {
--   "enabled": true,
--   "resource_uri": "https://use.hoop.dev/mcp",
--   "groups_claim": "groups"
-- }
ALTER TABLE authconfig ADD COLUMN mcp_auth_config JSONB NULL;

COMMIT;
