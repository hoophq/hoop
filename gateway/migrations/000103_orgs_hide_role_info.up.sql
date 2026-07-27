BEGIN;
SET search_path TO private;

-- Block reading connection/role secrets (envvars) from the API.
-- When enabled for an organization, the connection/role OpenAPI
-- conversion omits envvar values and updates preserve the stored
-- secrets instead of overwriting them with the masked payload.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS hide_role_info BOOLEAN NOT NULL DEFAULT false;

COMMIT;
