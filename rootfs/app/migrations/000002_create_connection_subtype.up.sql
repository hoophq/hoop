BEGIN;

SET search_path TO private;

ALTER TABLE private.connections ADD COLUMN subtype VARCHAR(64) NULL;

DROP FUNCTION public.update_connection;
DROP VIEW public.connections;

CREATE VIEW public.connections AS
    SELECT
        id, org_id, agent_id, legacy_agent_id, name, command,
        type, subtype, (SELECT envs FROM public.env_vars WHERE id = c.id) AS envs, created_at, updated_at
    FROM connections c;

CREATE FUNCTION public.update_connection(id uuid, org_id uuid, agent_id uuid, legacy_agent_id text, name text, command text[], type enum_connection_type, subtype text, envs JSON) RETURNS SETOF public.connections ROWS 1 AS $$
    WITH p AS (
        SELECT
            id as id,
            org_id as org_id,
            agent_id as agent_id,
            legacy_agent_id as legacy_agent_id,
            name as name,
            command as command,
            type as type,
            subtype as subtype,
            envs as envs
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, legacy_agent_id, name, command, type, subtype)
            (SELECT id, org_id, agent_id, legacy_agent_id, name, command, type, subtype FROM p)
        ON CONFLICT (org_id, name)
            DO UPDATE SET
                agent_id = (SELECT agent_id FROM p),
                legacy_agent_id = (SELECT legacy_agent_id FROM p),
                command = (SELECT command FROM p),
                subtype = (SELECT subtype FROM p),
                updated_at = NOW()
        RETURNING *
    ), envs AS (
        INSERT INTO env_vars (id, org_id, envs) VALUES
            ((SELECT id FROM conn), (SELECT org_id FROM conn), (SELECT envs FROM p))
            ON CONFLICT (id)
                DO UPDATE SET envs = (SELECT envs FROM p)
            RETURNING *
    )
    SELECT c.id, c.org_id, c.agent_id, c.legacy_agent_id, c.name, c.command, c.type, c.subtype, e.envs, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;

COMMIT;