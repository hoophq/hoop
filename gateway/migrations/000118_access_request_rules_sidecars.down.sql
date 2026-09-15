BEGIN;

SET search_path TO private;

ALTER TABLE access_request_rules DROP CONSTRAINT IF EXISTS access_request_rules_sidecar_targets;
ALTER TABLE access_request_rules DROP COLUMN IF EXISTS sidecar_names;

COMMIT;
