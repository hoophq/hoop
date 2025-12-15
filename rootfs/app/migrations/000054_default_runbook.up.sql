BEGIN;

SET search_path TO private;

INSERT INTO runbooks (org_id, repository_configs, created_at, updated_at)
SELECT 
    o.id,
    '{"demo-runbooks": {"git_url": "https://github.com/hoophq/demo-runbooks", "git_branch": "main"}}'::JSONB,
    NOW(),
    NOW()
FROM orgs o
LEFT JOIN runbooks r ON r.org_id = o.id
WHERE r.id IS NULL;

COMMIT;
