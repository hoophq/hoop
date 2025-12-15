BEGIN;

SET search_path TO private;

DELETE FROM runbooks
WHERE repository_configs = '{"demo-runbooks": {"git_url": "https://github.com/hoophq/demo-runbooks", "git_branch": "main"}}'::JSONB;

COMMIT;