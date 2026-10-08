BEGIN;

SET search_path TO private;

-- 000133 read "has an agent" from agents, but the gateway provisions the
-- `_default` agent key for the default org at every boot, so every
-- single-tenant install matched, control-plane installs included. An org that
-- has a sidecar and no agent of its own is a control-plane install: turn
-- experimental.agents back off. A `_default` key with a connection bound to
-- it serves `hoop run` agents and counts. updated_by: an admin's own setting
-- wins.
UPDATE org_feature_flags f
SET enabled = false, updated_at = now()
WHERE f.name = 'experimental.agents'
  AND f.enabled
  AND f.updated_by = 'migration'
  AND EXISTS (SELECT 1 FROM sidecars s WHERE s.org_id = f.org_id)
  AND NOT EXISTS (
    SELECT 1 FROM agents a
    WHERE a.org_id = f.org_id
      AND (a.name <> '_default'
           OR EXISTS (SELECT 1 FROM connections c WHERE c.org_id = a.org_id AND c.agent_id = a.id)));

COMMIT;
