BEGIN;

SET search_path TO private;

-- Restore what 000133 set: on for every org that has an agent row.
UPDATE org_feature_flags f
SET enabled = true, updated_at = now()
WHERE f.name = 'experimental.agents'
  AND NOT f.enabled
  AND f.updated_by = 'migration'
  AND EXISTS (SELECT 1 FROM agents a WHERE a.org_id = f.org_id);

COMMIT;
