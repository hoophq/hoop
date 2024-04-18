BEGIN;

SET search_path TO private;

DROP FUNCTION IF EXISTS public.update_connection;
DROP VIEW public.connections;
DROP VIEW IF EXISTS public.connection_status;
DROP TABLE private.connection_status;

ALTER TABLE private.connections ADD COLUMN status enum_connection_status DEFAULT 'offline';

CREATE VIEW public.connections AS
    SELECT id, org_id, agent_id, name, command, type, subtype,
        (SELECT envs FROM public.env_vars WHERE id = c.id) AS envs,
        status, managed_by, created_at, updated_at
    FROM connections c;

CREATE FUNCTION public.update_connection(params json) RETURNS SETOF public.connections ROWS 1 AS $$
    WITH user_input AS (
        SELECT
            (params->>'id')::UUID AS id,
            (params->>'org_id')::UUID AS org_id,
            (params->>'agent_id')::UUID AS agent_id,
            params->>'name' AS name,
            (
                SELECT array_agg(v)::TEXT[]
                FROM jsonb_array_elements_text((params->>'command')::JSONB) AS v
            ) AS command,
            (params->>'type')::private.enum_connection_type AS type,
            params->>'subtype' AS subtype,
            (params->>'envs')::JSONB AS envs,
            (params->>'status')::private.enum_connection_status AS status,
            params->>'managed_by' AS managed_by
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, name, command, type, subtype, status, managed_by)
            (SELECT id, org_id, agent_id, name, command, type, subtype, status, managed_by FROM user_input)
        ON CONFLICT (org_id, name)
            DO UPDATE SET
                agent_id = (SELECT agent_id FROM user_input),
                command = (SELECT command FROM user_input),
                type = (SELECT type FROM user_input),
                subtype = (SELECT subtype FROM user_input),
                status = (SELECT status FROM user_input),
                managed_by = (SELECT managed_by FROM user_input),
                updated_at = NOW()
        RETURNING *
    ), envs AS (
        INSERT INTO env_vars (id, org_id, envs) VALUES
            ((SELECT id FROM conn), (SELECT org_id FROM conn), (SELECT envs FROM user_input))
            ON CONFLICT (id)
                DO UPDATE SET envs = (SELECT envs FROM user_input)
            RETURNING *
    )
    SELECT c.id, c.org_id, c.agent_id, c.name, c.command, c.type, c.subtype, e.envs, c.status, c.managed_by, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;

COMMIT;