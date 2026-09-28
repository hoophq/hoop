BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS sidecar_listener_roles;

ALTER TABLE sidecars DROP CONSTRAINT IF EXISTS sidecars_resource_fk;
ALTER TABLE sidecars DROP COLUMN IF EXISTS resource_name;

-- Projected rows go with the prototype. Roles first: connections reference
-- resources by name.
DELETE FROM connections WHERE managed_by = 'sidecar';
DELETE FROM resources WHERE managed_by = 'sidecar';
ALTER TABLE resources DROP COLUMN IF EXISTS managed_by;

COMMIT;
