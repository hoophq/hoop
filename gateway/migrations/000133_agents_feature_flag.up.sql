-- experimental.agents picks the product the web app renders. Off by default:
-- an org that already registered an agent is a gateway install and keeps
-- the gateway product. ON CONFLICT: an admin's own setting wins.
INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_by)
SELECT DISTINCT org_id, 'experimental.agents', true, 'migration'
FROM private.agents
ON CONFLICT (org_id, name) DO NOTHING;

-- beta.sidecar_listeners left the catalog; its behavior is always on.
DELETE FROM private.org_feature_flags WHERE name = 'beta.sidecar_listeners';
