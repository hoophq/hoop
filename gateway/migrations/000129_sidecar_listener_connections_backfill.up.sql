BEGIN;

SET search_path TO private;

-- One connection per listener of every sidecar, as services.ProjectListeners
-- renders it, for the rows that predate the projection. From here on the
-- sidecar write path keeps them in step.
--
-- A listener this cannot mirror is left out, not refused: a migration that
-- fails leaves the database dirty and the gateway down, and the listener can
-- only be renamed through the gateway. The next write of that sidecar refuses
-- with the reason and names the listener (ErrSidecarListenerInvalid,
-- ErrSidecarConnectionNameTaken). Left out:
--   * a protocol with no connection type, or no listener name;
--   * a composed name that fails apivalidation.ValidateResourceName or
--     exceeds resources.name;
--   * a name a connection already has, this listener's own mirror included,
--     so the migration is a no-op the second time;
--   * a name two listeners compose to; the older sidecar keeps it.
CREATE TEMP TABLE _sidecar_mirrors ON COMMIT DROP AS
SELECT DISTINCT ON (org_id, name) *
FROM (
    SELECT
        s.org_id,
        s.id AS sidecar_id,
        s.created_at AS sidecar_created_at,
        l.ordinality,
        l.value->>'name' AS listener,
        s.name || '-' || (l.value->>'name') AS name,
        CASE l.value->>'protocol'
            WHEN 'postgres'   THEN 'database'
            WHEN 'mysql'      THEN 'database'
            WHEN 'mssql'      THEN 'database'
            WHEN 'mongodb'    THEN 'database'
            WHEN 'ssh'        THEN 'application'
            WHEN 'http'       THEN 'httpproxy'
            WHEN 'clickhouse' THEN 'custom'
            WHEN 'grpc'       THEN 'custom'
            WHEN 'spanner'    THEN 'custom'
        END AS type,
        CASE l.value->>'protocol'
            WHEN 'http' THEN 'httpproxy'
            ELSE l.value->>'protocol'
        END AS subtype
    FROM sidecars s
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(s.configuration->'listeners') = 'array'
             THEN s.configuration->'listeners' ELSE '[]'::jsonb END
    ) WITH ORDINALITY AS l(value, ordinality)
) m
WHERE COALESCE(m.listener, '') <> ''
  AND m.type IS NOT NULL
  AND m.name ~ '^[a-zA-Z0-9_]+([-.]?[a-zA-Z0-9_]+){2,253}$'
  AND length(m.name) <= 128
  AND NOT EXISTS (SELECT 1 FROM connections c WHERE c.org_id = m.org_id AND c.name = m.name)
ORDER BY org_id, name, sidecar_created_at, ordinality;

-- The mirror's own resource, as UpsertConnection defaults it. One left
-- behind by a connection that is gone is reused, with the mirror's type.
INSERT INTO resources (org_id, name, type, subtype)
SELECT org_id, name, type::enum_connection_type, subtype
FROM _sidecar_mirrors
ORDER BY sidecar_created_at, ordinality
ON CONFLICT (org_id, name) DO UPDATE SET type = EXCLUDED.type, subtype = EXCLUDED.subtype, updated_at = NOW();

INSERT INTO connections
    (org_id, name, resource_name, type, subtype, status, managed_by, sidecar_id, sidecar_listener,
     access_mode_runbooks, access_mode_exec, access_mode_connect, access_schema)
SELECT org_id, name, name, type::enum_connection_type, subtype, 'offline', 'sidecar', sidecar_id, listener,
       'disabled', 'disabled', 'enabled', 'disabled'
FROM _sidecar_mirrors
ORDER BY sidecar_created_at, ordinality;

COMMIT;
