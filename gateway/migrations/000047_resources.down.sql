BEGIN;

SET search_path TO private;

-- Merge all resource env_vars back into connection env_vars
WITH resource_connections AS (
    SELECT
        c.id AS connection_id,
        r.id AS resource_id,
        r_env.envs AS resource_envs
    FROM connections c
    JOIN resources r
      ON r.name = c.resource_name
     AND r.org_id = c.org_id
    JOIN env_vars r_env
      ON r_env.id = r.id
)
UPDATE env_vars c_env
SET envs = rc.resource_envs || c_env.envs
FROM resource_connections rc
WHERE c_env.id = rc.connection_id;

-- Remove env vars associated with resources
DELETE FROM env_vars WHERE id IN (SELECT r.id FROM resources r);

ALTER TABLE connections
    DROP CONSTRAINT connection_resource_fk,
    DROP COLUMN resource_name;

DROP TABLE resources;

COMMIT;