DELETE FROM private.org_feature_flags
WHERE name = 'experimental.review_notifications' AND updated_by = 'migration';
