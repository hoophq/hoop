-- experimental.review_notifications is on by default. An org on the agent
-- product with no sidecar opts in instead; an admin's own setting wins.
INSERT INTO private.org_feature_flags (org_id, name, enabled, updated_by)
SELECT f.org_id, 'experimental.review_notifications', false, 'migration'
FROM private.org_feature_flags f
WHERE f.name = 'experimental.agents'
  AND f.enabled
  AND NOT EXISTS (SELECT 1 FROM private.sidecars s WHERE s.org_id = f.org_id)
ON CONFLICT (org_id, name) DO NOTHING;
