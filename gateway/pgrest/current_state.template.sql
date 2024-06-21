-- This file contains the current state of postgrest functions and views.
--
-- Changing anything on this file should perform trigger a rollback
-- erasing the state and the recreating all instructions in this file.
-- --------------------------------

SET search_path TO {{ .target_schema }};

-- ORGS
--
CREATE VIEW orgs AS SELECT id, name, license, created_at FROM private.orgs;

CREATE VIEW audit AS
    SELECT id, org_id, event, metadata, created_by, created_at FROM private.audit;

-- LOGIN
--
CREATE VIEW login AS
    SELECT id, redirect, slack_id, outcome, updated_at, created_at FROM private.login;

-- USERS
--
CREATE VIEW users AS
    SELECT id, org_id, subject, email, name, picture, verified, status, slack_id, created_at, updated_at
    FROM private.users;

CREATE VIEW user_groups AS SELECT org_id, user_id, service_account_id, name FROM private.user_groups;

CREATE FUNCTION groups(users) RETURNS TEXT[] AS $$
    SELECT ARRAY(
        SELECT name FROM user_groups
        WHERE org_id = $1.org_id
        AND user_id = $1.id
    )
$$ LANGUAGE SQL;

CREATE FUNCTION update_users(params json) RETURNS SETOF users AS $$
    WITH user_input AS (
        SELECT
            (params->>'id')::UUID AS id,
            (params->>'org_id')::UUID AS org_id,
            params->>'subject' AS subject,
            params->>'email' AS email,
            params->>'name' AS name,
            params->>'picture' AS picture,
            (params->>'verified')::BOOL AS verified,
            (params->>'status')::private.enum_user_status AS status,
            params->>'slack_id' AS slack_id
    ), upsert_users AS (
        INSERT INTO users (id, org_id, subject, email, name, picture, verified, status, slack_id)
            (SELECT id, org_id, subject, email, name, picture, verified, status, slack_id FROM user_input)
        ON CONFLICT (id)
            DO UPDATE SET
                subject = (SELECT subject FROM user_input),
                name = (SELECT name FROM user_input),
                status = (SELECT status FROM user_input),
                verified = (SELECT verified FROM user_input),
                slack_id = (SELECT slack_id FROM user_input),
                picture = (SELECT picture FROM user_input),
                updated_at = NOW()
        RETURNING *
    ), grps AS (
        SELECT
            (params->>'org_id')::UUID AS org_id,
            (params->>'id')::UUID AS user_id,
            jsonb_array_elements_text((params->>'groups')::JSONB) AS name
    ), update_user_groups AS (
        INSERT INTO user_groups (org_id, user_id, name)
            SELECT org_id, user_id, name FROM grps
            ON CONFLICT DO NOTHING
    ), del_grousp AS (
        DELETE FROM user_groups
        WHERE user_id = (params->>'id')::UUID
        AND org_id = (params->>'org_id')::UUID
        AND name NOT IN (SELECT name::TEXT FROM grps)
    )
    SELECT * FROM upsert_users
$$ LANGUAGE SQL;

CREATE VIEW serviceaccounts AS
    SELECT id, org_id, subject, name, status, created_at, updated_at FROM private.service_accounts;

CREATE FUNCTION groups(serviceaccounts) RETURNS TEXT[] AS $$
    SELECT ARRAY(
        SELECT name FROM user_groups
        WHERE org_id = $1.org_id
        AND service_account_id = $1.id
    )
$$ LANGUAGE SQL;

CREATE FUNCTION
    update_serviceaccounts(id UUID, org_id UUID, subject TEXT, name TEXT, status private.enum_service_account_status, groups VARCHAR(100)[]) RETURNS SETOF serviceaccounts AS $$
    WITH params AS (
        SELECT
            id AS id,
            org_id AS org_id,
            subject AS subject,
            name AS name,
            status AS status
    ), upsert_svc_account AS (
        INSERT INTO serviceaccounts (id, org_id, subject, name, status)
            (SELECT id, org_id, subject, name, status FROM params)
        ON CONFLICT (id)
            DO UPDATE SET name = (SELECT name FROM params), status = (SELECT status FROM params), updated_at = NOW()
        RETURNING *
    ), grps AS (
        SELECT
            org_id AS org_id,
            id AS service_account_id,
            UNNEST(groups) AS name
    ), update_user_groups AS (
        INSERT INTO user_groups (org_id, service_account_id, name)
            SELECT org_id, service_account_id, name FROM grps
            ON CONFLICT DO NOTHING
    ), del_groups AS (
        DELETE FROM user_groups
        WHERE service_account_id = id
        AND org_id = org_id
        AND name NOT IN (SELECT name FROM grps)
    )
    SELECT * FROM upsert_svc_account
$$ LANGUAGE SQL;

-- AGENTS
--
CREATE VIEW agents AS
    SELECT id, org_id, name, mode, key, key_hash, metadata, status, created_at, updated_at
    FROM private.agents;

-- CONNECTIONS
--
CREATE VIEW env_vars AS SELECT id, org_id, envs FROM private.env_vars;

CREATE VIEW connections AS
    SELECT id, org_id, agent_id, name, command, type, subtype,
        (SELECT envs FROM env_vars WHERE id = c.id) AS envs,
        status, managed_by, _tags AS tags, created_at, updated_at
    FROM private.connections c;

CREATE FUNCTION agents(connections) RETURNS SETOF agents ROWS 1 AS $$
  SELECT * FROM agents WHERE id = $1.agent_id
$$ stable language sql;

CREATE FUNCTION update_connection(params json) RETURNS SETOF connections ROWS 1 AS $$
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
            params->>'managed_by' AS managed_by,
            (
                SELECT array_agg(v)::TEXT[]
                FROM jsonb_array_elements_text((params->>'tags')::JSONB) AS v
            ) AS tags
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, name, command, type, subtype, status, managed_by, tags)
            (SELECT id, org_id, agent_id, name, command, type, subtype, status, managed_by, tags FROM user_input)
        ON CONFLICT (org_id, name)
            DO UPDATE SET
                agent_id = (SELECT agent_id FROM user_input),
                command = (SELECT command FROM user_input),
                type = (SELECT type FROM user_input),
                subtype = (SELECT subtype FROM user_input),
                status = (SELECT status FROM user_input),
                managed_by = (SELECT managed_by FROM user_input),
                tags = (SELECT tags FROM user_input),
                updated_at = NOW()
        RETURNING *
    ), envs AS (
        INSERT INTO env_vars (id, org_id, envs) VALUES
            ((SELECT id FROM conn), (SELECT org_id FROM conn), (SELECT envs FROM user_input))
            ON CONFLICT (id)
                DO UPDATE SET envs = (SELECT envs FROM user_input)
            RETURNING *
    )
    SELECT c.id, c.org_id, c.agent_id, c.name, c.command, c.type, c.subtype, e.envs, c.status, c.managed_by, c.tags, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;

-- PLUGINS
--
CREATE VIEW plugins AS SELECT id, org_id, name, source FROM private.plugins;

CREATE FUNCTION env_vars(plugins) RETURNS SETOF env_vars ROWS 1 AS $$
  SELECT * FROM env_vars WHERE id = $1.id
$$ stable language sql;

CREATE VIEW plugin_connections AS
    SELECT id, org_id, plugin_id, connection_id, enabled, config FROM private.plugin_connections;

CREATE FUNCTION env_vars(plugin_connections) RETURNS SETOF env_vars ROWS 1 AS $$
  SELECT * FROM env_vars WHERE id = $1.plugin_id
$$ stable language sql;

-- SESSIONS
--
CREATE VIEW sessions AS
    SELECT
        id, org_id, labels, connection, connection_type, verb, user_id, user_name, user_email, status,
        blob_input_id, blob_stream_id, metadata, metrics, created_at, ended_at
    FROM private.sessions;

CREATE VIEW blobs AS
    SELECT id, org_id, type, pg_column_size(blob_stream) AS size, blob_stream, created_at FROM private.blobs;

CREATE FUNCTION blob_input(sessions) RETURNS SETOF blobs ROWS 1 AS $$
  SELECT * FROM blobs WHERE id = $1.blob_input_id
$$ stable language sql;

CREATE FUNCTION blob_stream(sessions) RETURNS SETOF blobs ROWS 1 AS $$
  SELECT * FROM blobs WHERE id = $1.blob_stream_id
$$ stable language sql;

-- SESSION REPORTS
--

CREATE FUNCTION session_report(p json) RETURNS TABLE (resource text, info_type text, redact_total int, transformed_bytes int) AS $$
    WITH info_types AS (
        SELECT id, info_type, redact_total::numeric
        FROM
            sessions,
            jsonb_each_text(metrics->'data_masking'->'info_types') AS kv(info_type, redact_total)
        WHERE org_id::TEXT = p->>'org_id'
        AND metrics is not null
    ), metrics AS (
        SELECT
            CASE p->>'group_by'
                WHEN 'connection_name' THEN s.connection
                WHEN 'id' THEN s.id::text
                WHEN 'user_email' THEN s.user_email
                WHEN 'connection_type' THEN s.connection_type::text
            END AS resource,
            i.info_type,
            SUM(i.redact_total) AS redact_total,
            SUM((metrics->'data_masking'->'transformed_bytes')::INT) AS transformed_bytes
        FROM sessions s
        INNER JOIN info_types i ON s.id = i.id
        WHERE s.org_id::TEXT = p->>'org_id'
        AND metrics is not null
        AND ended_at BETWEEN TO_TIMESTAMP(p->>'start_date', 'YYYY-MM-DD') AND TO_TIMESTAMP(p->>'end_date', 'YYYY-MM-DD')
        AND CASE WHEN p->>'id' != '' THEN s.id::TEXT = p->>'id' ELSE true END
        AND CASE WHEN p->>'connection_name' != '' THEN s.connection = p->>'connection_name' ELSE true END
        AND CASE WHEN p->>'connection_type' != '' THEN s.connection_type::TEXT = p->>'connection_type'::TEXT ELSE true END
        AND CASE WHEN p->>'verb' != '' THEN s.verb::TEXT = p->>'verb' ELSE true END
        AND CASE WHEN p->>'user_email' != '' THEN s.user_email = p->>'user_email' ELSE true END
        GROUP BY 1, 2
    ) SELECT * FROM metrics
$$ LANGUAGE SQL;

-- REVIEWS
--
CREATE VIEW reviews AS
    SELECT
        id, org_id, session_id, connection_id, connection_name, type, blob_input_id,
        input_env_vars, input_client_args, access_duration_sec, status,
        owner_id, owner_email, owner_name, owner_slack_id, created_at, revoked_at
    FROM private.reviews;

CREATE VIEW review_groups AS
    SELECT
        id, org_id, review_id, group_name, status,
        owner_id, owner_email, owner_name, owner_slack_id, reviewed_at
    FROM private.review_groups;

CREATE FUNCTION blob_input(reviews) RETURNS SETOF blobs ROWS 1 AS $$
  SELECT * FROM blobs WHERE id = $1.blob_input_id
$$ stable language sql;

-- PROXYMANAGER
--
CREATE VIEW proxymanager_state AS
    SELECT id, org_id, status, connection, port, access_duration, metadata, connected_at
    FROM private.proxymanager_state;

-- -----------------
-- ROLE PERMISSIONS
-- -----------------

-- drop all privileges of the role and then the role itself
DO $$
    DECLARE
        role_count int;
BEGIN
    SELECT COUNT(*) INTO role_count FROM pg_roles WHERE rolname = '{{ .pgrest_role }}';
    IF role_count > 0 THEN
        REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA {{ .target_schema }} FROM {{ .pgrest_role }};
        REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA {{ .target_schema }} FROM {{ .pgrest_role }};
        REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA {{ .target_schema }} FROM {{ .pgrest_role }};
        REVOKE USAGE ON SCHEMA private FROM {{ .pgrest_role }};
        REVOKE USAGE ON SCHEMA {{ .target_schema }} FROM {{ .pgrest_role }};
        DROP ROLE IF EXISTS {{ .pgrest_role }};
    ELSE
        RAISE NOTICE 'role {{ .pgrest_role }} not exists';
    END IF;
END$$;

-- add permissions
CREATE ROLE {{ .pgrest_role }} LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOSUPERUSER;
COMMENT ON ROLE {{ .pgrest_role }} IS 'Used to authenticate requests in postgrest';
GRANT usage ON SCHEMA {{ .target_schema }} TO {{ .pgrest_role }};
GRANT usage ON SCHEMA private TO {{ .pgrest_role }};
GRANT SELECT, INSERT ON orgs TO {{ .pgrest_role }};
GRANT INSERT, SELECT, UPDATE on login TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON users TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON user_groups TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON serviceaccounts TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON connections TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON env_vars TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON agents TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON plugin_connections TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON plugins TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON sessions TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON blobs TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON reviews TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON review_groups TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE, DELETE ON proxymanager_state TO {{ .pgrest_role }};
GRANT SELECT, INSERT, UPDATE ON audit TO {{ .pgrest_role }};

-- allow the main role to impersonate the apiuser role
GRANT {{ .pgrest_role }} TO {{ .pg_app_user }};
