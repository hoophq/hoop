DELETE FROM private.org_feature_flags
WHERE name = 'experimental.agents' AND updated_by = 'migration';
-- beta.sidecar_listeners rows are not restored: the flag is gone from the catalog.
