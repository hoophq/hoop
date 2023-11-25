-- phase 2

CREATE SCHEMA IF NOT EXISTS private;
SET search_path TO private;

CREATE EXTENSION "uuid-ossp";

CREATE TYPE enum_login_outcome AS ENUM ('success', 'error', 'pending_review', 'email_mismatch');
CREATE TABLE login(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,

    redirect VARCHAR(255) NULL,
    outcome enum_login_outcome NULL,
    slack_id VARCHAR(50) NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE orgs(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,

    name VARCHAR(100) UNIQUE,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TYPE enum_user_status AS ENUM ('active', 'reviewing', 'inactive');
CREATE TABLE users(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    subject VARCHAR(255) NOT NULL UNIQUE,

    email VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    verified BOOLEAN DEFAULT FALSE, -- invited user
    status enum_user_status NOT NULL,
    slack_id VARCHAR(50) NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TYPE enum_service_account_status AS ENUM ('active', 'inactive');
CREATE TABLE service_accounts(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    subject VARCHAR(255) NOT NULL UNIQUE,

    name VARCHAR(255) NOT NULL,
    status enum_service_account_status DEFAULT 'active',

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE user_groups(
    org_id UUID NOT NULL REFERENCES orgs (id),
    user_id UUID NULL REFERENCES users(id) ON DELETE CASCADE,
    service_account_id UUID NULL REFERENCES service_accounts(id) ON DELETE CASCADE,

    name VARCHAR(100) NOT NULL,

    UNIQUE(user_id, name),
    UNIQUE(service_account_id, name)
);

CREATE TYPE enum_agent_mode AS ENUM ('standard', 'embedded');
CREATE TYPE enum_agent_status AS ENUM ('CONNECTED', 'DISCONNECTED');

CREATE TABLE agents(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    name VARCHAR(63) CHECK (name ~ '^[a-z]([-a-z0-9]*[a-z0-9])?$'),
    mode enum_agent_mode NOT NULL,
    token VARCHAR(255) NOT NULL,
    status enum_agent_status DEFAULT 'DISCONNECTED',
    metadata JSONB NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, name)
);

CREATE TYPE enum_clientkeys_status AS ENUM ('active', 'inactive');
CREATE TABLE clientkeys(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    name VARCHAR(255) NOT NULL,
    status enum_clientkeys_status DEFAULT 'inactive',
    dsn_hash VARCHAR(255) NOT NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TYPE enum_connection_type AS ENUM ('command-line', 'postgres', 'mysql', 'mssql', 'tcp');
CREATE TABLE connections(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    agent_id UUID NULL,
    -- maintain compatibility with embedded flows
    -- that uses the agent id as non uuid
    legacy_agent_id VARCHAR(255) NULL,

    name VARCHAR(128) NOT NULL,
    command text[] NULL,
    type enum_connection_type NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, name)
);

CREATE TABLE plugins(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    name VARCHAR(128) NOT NULL,
    source VARCHAR(128) NULL,
    priority int DEFAULT 0,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(org_id, name)
);

CREATE TABLE plugin_connections(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NULL REFERENCES orgs (id),
    plugin_id UUID NOT NULL REFERENCES plugins (id) ON DELETE CASCADE,
    connection_id UUID NOT NULL REFERENCES connections (id) ON DELETE CASCADE,

    enabled BOOLEAN DEFAULT TRUE,
    config TEXT[] NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(plugin_id, connection_id)
);

CREATE TABLE env_vars(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    envs JSONB NULL
);

CREATE TYPE enum_session_status AS ENUM ('open', 'ready', 'done');
CREATE TYPE enum_session_verb AS ENUM ('connect', 'exec');
CREATE TABLE sessions(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    connection VARCHAR(128) NOT NULL,
    connection_type enum_connection_type NOT NULL,
    -- input TEXT NULL,
    verb enum_session_verb NOT NULL,
    labels JSONB NULL,
    user_id VARCHAR(255) NULL,
    user_name VARCHAR(255) NULL,
    user_email VARCHAR(255) NULL,
    -- event_stream JSONB NULL,
    -- blob_size int DEFAULT 0,
    status enum_session_status NOT NULL,
    blob_input_id UUID NULL,
    blob_stream_id UUID NULL,
    -- blob-size, dlp count
    metadata JSONB NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    ended_at TIMESTAMP NULL,

    UNIQUE(org_id, id)
);

CREATE TYPE enum_blob_type AS ENUM ('review-input', 'session-input', 'session-stream');
CREATE table blobs(
    -- refers to any resource/table that needs to manage blobs
    id UUID DEFAULT uuid_generate_v4(),
    org_id UUID NOT NULL REFERENCES orgs (id),

    type enum_blob_type NOT NULL,
    blob_stream JSONB NOT NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(org_id, id)
);

CREATE TYPE enum_reviews_status AS ENUM ('PENDING', 'APPROVED', 'REVOKED', 'REJECTED', 'PROCESSING', 'EXECUTED', 'UNKNOWN');
CREATE TYPE enum_reviews_type AS ENUM ('onetime', 'jit');
CREATE TABLE reviews(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    session_id UUID NULL,
    connection_id UUID NULL,
    connection_name VARCHAR(128) NOT NULL,

    type enum_reviews_type NOT NULL,
    blob_input_id UUID NULL,
    input_env_vars JSONB NULL,
    input_client_args TEXT[] NULL,
    access_duration_sec INT DEFAULT 0,
    status enum_reviews_status NOT NULL,

    owner_id VARCHAR(255) NOT NULL,
    owner_email VARCHAR(255) NOT NULL,
    owner_name VARCHAR(255) NULL,
    owner_slack_id VARCHAR(50) NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    revoked_at TIMESTAMP NULL,

    UNIQUE(org_id, session_id)
);

CREATE TABLE review_groups(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    review_id UUID NOT NULL REFERENCES reviews (id) ON DELETE CASCADE,

    group_name VARCHAR(100) NOT NULL,
    status enum_reviews_status NOT NULL,

    owner_id VARCHAR(255) NULL,
    owner_email VARCHAR(255) NULL,
    owner_name VARCHAR(255) NULL,
    owner_slack_id VARCHAR(50) NULL,

    reviewed_at TIMESTAMP NULL
);

CREATE TYPE enum_proxymanager_status AS ENUM ('ready', 'connected', 'disconnected');
CREATE TABLE proxymanager_state(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    status enum_proxymanager_status DEFAULT 'ready',
    connection VARCHAR(128) NOT NULL,
    port VARCHAR(5) NOT NULL,
    access_duration int DEFAULT 0,
    metadata JSONB NULL,

    connected_at TIMESTAMP DEFAULT NOW()
);

-- org
CREATE OR REPLACE VIEW public.orgs AS
    SELECT id, name, created_at FROM orgs;

-- users / login
CREATE OR REPLACE VIEW public.login AS
    SELECT id, redirect, slack_id, outcome FROM login;

CREATE OR REPLACE VIEW public.users AS
    SELECT
        u.id, u.org_id, u.subject, u.email, u.name, u.verified, u.status, u.slack_id,
        coalesce(array_agg(g.name) filter (WHERE g.name IS NOT NULL), NULL) AS groups,
        u.created_at, u.updated_at
    FROM users u
    LEFT JOIN user_groups g
        ON u.id = g.user_id
    GROUP BY u.id;

CREATE OR REPLACE VIEW public.users_update AS
    SELECT
        id, org_id, subject, email, name,
        verified, status, slack_id,
        created_at, updated_at
    FROM users;

CREATE OR REPLACE VIEW public.user_groups_update AS
    SELECT org_id, user_id, service_account_id, name FROM user_groups;


CREATE OR REPLACE FUNCTION public.org(public.users) RETURNS SETOF public.orgs ROWS 1 AS $$
  SELECT * FROM public.orgs WHERE id = $1.org_id
$$ stable language sql;

CREATE OR REPLACE FUNCTION public.update_groups(org_id UUID, user_id UUID, groups VARCHAR(100)[]) RETURNS SETOF public.user_groups_update AS $$
    DELETE FROM public.user_groups_update where user_id = user_id;
    WITH groups AS (
        SELECT org_id as org_id, user_id as user_id, UNNEST(groups) as name
    )
    INSERT INTO public.user_groups_update (org_id, user_id, name)
    SELECT org_id, user_id, name FROM groups RETURNING *;
$$ LANGUAGE SQL;

-- service accounts
CREATE OR REPLACE VIEW public.serviceaccounts AS
    SELECT
        id, org_id, subject, name, status, created_at, updated_at
    FROM service_accounts;

CREATE OR REPLACE FUNCTION
    public.update_serviceaccounts(id UUID, org_id UUID, subject TEXT, name TEXT, status enum_service_account_status, groups VARCHAR(100)[]) RETURNS SETOF public.serviceaccounts AS $$
    WITH params AS (
        SELECT
            id AS id,
            org_id AS org_id,
            subject AS subject,
            name AS name,
            status AS status
    ), upsert_svc_account AS (
        INSERT INTO public.serviceaccounts (id, org_id, subject, name, status)
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
        INSERT INTO public.user_groups_update (org_id, service_account_id, name)
            SELECT org_id, service_account_id, name FROM grps
            ON CONFLICT DO NOTHING
    ), del_grousp AS (
        DELETE FROM public.user_groups_update
        WHERE service_account_id = id
        AND org_id = org_id
        AND name NOT IN (SELECT name FROM grps)
    )
    SELECT * FROM upsert_svc_account
$$ LANGUAGE SQL;

CREATE OR REPLACE FUNCTION public.groups(public.serviceaccounts) RETURNS TEXT[] AS $$
  SELECT coalesce(array_agg(g.name) filter (WHERE g.name IS NOT NULL), NULL)
  FROM public.user_groups_update g
  WHERE service_account_id = $1.id
  AND org_id = $1.org_id
$$ LANGUAGE SQL;

-- agents
CREATE OR REPLACE VIEW public.agents AS
    SELECT
        id, org_id, name, mode, token, metadata, status, created_at, updated_at
    FROM agents;

CREATE OR REPLACE FUNCTION public.org(public.agents) RETURNS SETOF public.orgs ROWS 1 AS $$
  SELECT * FROM public.orgs WHERE id = $1.org_id
$$ stable language sql;

-- connections
CREATE OR REPLACE VIEW public.env_vars AS
    SELECT id, org_id, envs FROM env_vars;

CREATE OR REPLACE VIEW public.connections AS
    SELECT id, org_id, agent_id, legacy_agent_id, name, command, type, (SELECT envs FROM public.env_vars WHERE id = c.id) AS envs, created_at, updated_at
    FROM connections c;

CREATE OR REPLACE FUNCTION public.update_connection(id uuid, org_id uuid, agent_id uuid, legacy_agent_id text, name text, command text[], type enum_connection_type, envs JSON) RETURNS SETOF public.connections ROWS 1 AS $$
    WITH p AS (
        SELECT
            id as id,
            org_id as org_id,
            agent_id as agent_id,
            legacy_agent_id as legacy_agent_id,
            name as name,
            command as command,
            type as type,
            envs as envs
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, legacy_agent_id, name, command, type)
            (SELECT id, org_id, agent_id, legacy_agent_id, name, command, type FROM p)
        ON CONFLICT (org_id, name)
            DO UPDATE SET
                agent_id = (SELECT agent_id FROM p),
                legacy_agent_id = (SELECT legacy_agent_id FROM p),
                command = (SELECT command FROM p),
                updated_at = NOW()
        RETURNING *
    ), envs AS (
    INSERT INTO env_vars (id, org_id, envs) VALUES ((SELECT id FROM conn), (SELECT org_id FROM conn), (SELECT envs FROM p))
        ON CONFLICT (id)
        DO UPDATE SET envs = (SELECT envs FROM p)
        RETURNING *
    )
    SELECT c.id, c.org_id, c.agent_id, c.legacy_agent_id, c.name, c.command, c.type, e.envs, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;

-- plugins
CREATE OR REPLACE VIEW public.plugins AS
    SELECT id, org_id, name, source
    FROM plugins;

CREATE OR REPLACE FUNCTION public.env_vars(public.plugins) RETURNS SETOF public.env_vars ROWS 1 AS $$
  SELECT * FROM public.env_vars WHERE id = $1.id
$$ stable language sql;

CREATE OR REPLACE VIEW public.plugin_connections AS
    SELECT id, org_id, plugin_id, connection_id, enabled, config
    FROM plugin_connections;

CREATE OR REPLACE FUNCTION public.env_vars(public.plugin_connections) RETURNS SETOF public.env_vars ROWS 1 AS $$
  SELECT * FROM public.env_vars WHERE id = $1.plugin_id
$$ stable language sql;

-- sessions
CREATE OR REPLACE VIEW public.sessions AS
    SELECT
        id, org_id, labels, connection, connection_type, verb, user_id, user_name, user_email, status,
        blob_input_id, blob_stream_id, metadata, created_at, ended_at
    FROM sessions;

CREATE OR REPLACE VIEW public.blobs AS
    SELECT id, org_id, type, pg_column_size(blob_stream) AS size, blob_stream, created_at
    FROM blobs;

CREATE OR REPLACE FUNCTION public.blob_input(public.sessions) RETURNS SETOF public.blobs ROWS 1 AS $$
  SELECT * FROM public.blobs WHERE id = $1.blob_input_id
$$ stable language sql;

CREATE OR REPLACE FUNCTION public.blob_stream(public.sessions) RETURNS SETOF public.blobs ROWS 1 AS $$
  SELECT * FROM public.blobs WHERE id = $1.blob_stream_id
$$ stable language sql;

-- reviews
CREATE OR REPLACE VIEW public.reviews AS
    SELECT
        id, org_id, session_id, connection_id, connection_name, type, blob_input_id,
        input_env_vars, input_client_args, access_duration_sec, status,
        owner_id, owner_email, owner_name, owner_slack_id, created_at, revoked_at
    FROM reviews;

CREATE OR REPLACE VIEW public.review_groups AS
    SELECT
        id, org_id, review_id, group_name, status,
        owner_id, owner_email, owner_name, owner_slack_id, reviewed_at
    FROM review_groups;

CREATE OR REPLACE FUNCTION public.blob_input(public.reviews) RETURNS SETOF public.blobs ROWS 1 AS $$
  SELECT * FROM public.blobs WHERE id = $1.blob_input_id
$$ stable language sql;

-- proxymanager
CREATE OR REPLACE VIEW public.proxymanager_state AS
    SELECT
        id, org_id, status, connection, port, access_duration, metadata, connected_at
    FROM proxymanager_state;

-- clientkeys
CREATE OR REPLACE VIEW public.clientkeys AS
    SELECT
        id, org_id, name, status, dsn_hash, created_at, updated_at
    FROM clientkeys;

GRANT webuser TO hoopadm;
GRANT usage ON SCHEMA public TO webuser;
GRANT usage ON SCHEMA private TO webuser;

GRANT SELECT, INSERT ON public.orgs to webuser;
GRANT INSERT, SELECT, UPDATE on public.login to webuser;
GRANT SELECT, INSERT, UPDATE ON public.users to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.users_update to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_groups_update to webuser;
GRANT SELECT, INSERT, UPDATE ON public.serviceaccounts to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.connections to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.env_vars to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.agents to webuser;
GRANT SELECT ON public.plugin_connections to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.plugin_connections to webuser;
GRANT SELECT, INSERT, UPDATE ON public.plugins to webuser;
GRANT SELECT, INSERT, UPDATE ON public.sessions to webuser;
GRANT SELECT, INSERT, UPDATE ON public.blobs to webuser;
GRANT SELECT, INSERT, UPDATE ON public.reviews to webuser;
GRANT SELECT, INSERT, UPDATE ON public.review_groups to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.proxymanager_state to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.clientkeys to webuser;


INSERT INTO plugins (id, org_id, name) VALUES
    ('DC0F40AF-12A8-495A-974D-FF03DD921406', (SELECT id FROM private.orgs), 'dlp'),
    ('F73A6FD7-A84C-4E00-A3AE-475150392224', (SELECT id FROM private.orgs), 'audit');

INSERT INTO plugin_connections (plugin_id, org_id, connection_id, config) VALUES
    ('F73A6FD7-A84C-4E00-A3AE-475150392224', (SELECT id FROM private.orgs), '90a612d8-4962-4426-9d57-01ed4ba110ce', ARRAY['myconfig01']),
    ('F73A6FD7-A84C-4E00-A3AE-475150392224', (SELECT id FROM private.orgs), '02cfd4a1-d108-4daa-b06f-54b9b7fed66d', ARRAY['myconfig02']);

INSERT INTO env_vars (id, org_id, envs) VALUES
    ('F73A6FD7-A84C-4E00-A3AE-475150392224', (SELECT id FROM private.orgs), '{"GIT_URL": "haha", "GIT_PASS": "pwd"}');

INSERT INTO agents (org_id, id, name, mode, token, status)
    VALUES ((SELECT id from private.orgs), '75122BCE-F957-49EB-A812-2AB60977CD9F', 'dev', 'standard', '7854115b1ae448fec54d8bf50d3ce223e30c1c933edcd12767692574f326df57', 'DISCONNECTED');

INSERT INTO clientkeys (org_id, name, status, dsn_hash) VALUES
    ((SELECT id from private.orgs), 'heroku', 'active', '99bce555b51d55e6b665dd3ad452b0f8b3a466f3cb9b095d307506b16eb77fb1');
