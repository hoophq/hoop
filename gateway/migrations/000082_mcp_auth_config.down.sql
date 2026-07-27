BEGIN;

SET search_path TO private;

ALTER TABLE authconfig DROP COLUMN IF EXISTS mcp_auth_config;

COMMIT;
