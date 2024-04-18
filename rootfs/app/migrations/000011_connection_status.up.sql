BEGIN;

SET search_path TO private;

CREATE TYPE enum_connection_status AS ENUM ('online', 'offline');
CREATE TABLE connection_status(
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES orgs (id),
    status enum_connection_status DEFAULT 'offline',
    updated_at TIMESTAMP DEFAULT NOW()
);

-- recreate connection function and view
DROP FUNCTION IF EXISTS public.update_connection;
DROP VIEW public.connections;

-- This column was used because of a flawed feature in XTDB that utilized
-- the agent_id as a non-UUID value. It was used in conjunction with the client keys feature,
-- which is now deprecated and no longer has any related data.
ALTER TABLE private.connections DROP COLUMN legacy_agent_id;

CREATE VIEW public.connections AS
    SELECT id, org_id, agent_id, name, command, type, subtype,
        (SELECT envs FROM public.env_vars WHERE id = c.id) AS envs,
        COALESCE((SELECT status FROM connection_status WHERE connection_id = c.id), 'offline') AS status,
        managed_by, created_at, updated_at
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
            params->>'managed_by' AS managed_by
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, name, command, type, subtype, managed_by)
            (SELECT id, org_id, agent_id, name, command, type, subtype, managed_by FROM user_input)
        ON CONFLICT (org_id, name)
            DO UPDATE SET
                agent_id = (SELECT agent_id FROM user_input),
                command = (SELECT command FROM user_input),
                type = (SELECT type FROM user_input),
                subtype = (SELECT subtype FROM user_input),
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
    SELECT c.id, c.org_id, c.agent_id, c.name, c.command, c.type, c.subtype, e.envs,
        'offline'::private.enum_connection_status AS status, c.managed_by, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;

COMMIT;