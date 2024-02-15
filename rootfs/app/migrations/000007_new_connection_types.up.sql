BEGIN;

SET search_path TO private;

ALTER TYPE private.enum_connection_type ADD VALUE IF NOT EXISTS 'custom';
ALTER TYPE private.enum_connection_type ADD VALUE IF NOT EXISTS 'database';

DROP FUNCTION IF EXISTS public.update_connection;
DROP VIEW public.connections;
CREATE VIEW public.connections AS
    SELECT id, org_id, agent_id, legacy_agent_id, name, command, type, subtype, (SELECT envs FROM public.env_vars WHERE id = c.id) AS envs, created_at, updated_at
    FROM connections c;

CREATE FUNCTION public.update_connection(params json) RETURNS SETOF public.connections ROWS 1 AS $$
    WITH user_input AS (
        SELECT
            (params->>'id')::UUID AS id,
            (params->>'org_id')::UUID AS org_id,
            (params->>'agent_id')::UUID AS agent_id,
            (params->>'legacy_agent_id')::TEXT AS legacy_agent_id,
            params->>'name' AS name,
            (
                SELECT array_agg(v)::TEXT[]
                FROM jsonb_array_elements_text((params->>'command')::JSONB) AS v
            ) AS command,
            (params->>'type')::private.enum_connection_type AS type,
            params->>'subtype' AS subtype,
            (params->>'envs')::JSONB AS envs
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, legacy_agent_id, name, command, type, subtype)
            (SELECT id, org_id, agent_id, legacy_agent_id, name, command, type, subtype FROM user_input)
        ON CONFLICT (org_id, name)
            DO UPDATE SET
                agent_id = (SELECT agent_id FROM user_input),
                legacy_agent_id = (SELECT legacy_agent_id FROM user_input),
                command = (SELECT command FROM user_input),
                type = (SELECT type FROM user_input),
                subtype = (SELECT subtype FROM user_input),
                updated_at = NOW()
        RETURNING *
    ), envs AS (
        INSERT INTO env_vars (id, org_id, envs) VALUES
            ((SELECT id FROM conn), (SELECT org_id FROM conn), (SELECT envs FROM user_input))
            ON CONFLICT (id)
                DO UPDATE SET envs = (SELECT envs FROM user_input)
            RETURNING *
    )
    SELECT c.id, c.org_id, c.agent_id, c.legacy_agent_id, c.name, c.command, c.type, c.subtype, e.envs, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;

COMMIT;