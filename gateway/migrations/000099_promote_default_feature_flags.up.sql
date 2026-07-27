-- ENG-470: promote default-enabled experimental flags to permanent features.
-- experimental.event_routing, experimental.agent_async_ssh and
-- experimental.hoop_tunnel were removed from the feature-flag catalog and
-- their behavior is now always on. Delete any per-org overrides so no stale
-- rows linger for flag names the application no longer recognizes.
DELETE FROM private.org_feature_flags
WHERE name IN (
    'experimental.event_routing',
    'experimental.agent_async_ssh',
    'experimental.hoop_tunnel'
);
