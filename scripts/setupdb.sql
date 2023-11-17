-- phase 2

CREATE SCHEMA IF NOT EXISTS private;
SET search_path TO private;

CREATE EXTENSION "uuid-ossp";

CREATE TYPE enum_login_outcome AS ENUM ('success', 'error', 'pending_review', 'email_mismatch');
CREATE TABLE login(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    
    redirect VARCHAR(255) NULL,
    outcome enum_login_outcome NULL,
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
    user_id UUID NULL REFERENCES users(id),
    service_account_id UUID NULL REFERENCES service_accounts(id),

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

CREATE TYPE enum_client_keys_status AS ENUM ('active', 'inactive');
CREATE TABLE client_keys(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    
    name VARCHAR(255) NOT NULL,
    mode enum_agent_mode NOT NULL,
    status enum_client_keys_status DEFAULT 'inactive',
    dsn_hash VARCHAR(255) NOT NULL,
    
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TYPE enum_connection_type AS ENUM ('command-line', 'postgres', 'mysql', 'mssql', 'tcp');
CREATE TABLE connections(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    agent_id UUID NULL,
    
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

    name VARCHAR(50) NOT NULL,
    source VARCHAR(50) NULL,
    priority int DEFAULT 0,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE connection_plugins(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    plugin_id UUID NOT NULL REFERENCES plugins (id),
    connection_id UUID NOT NULL REFERENCES connections (id),

    config TEXT[] NULL,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE env_vars(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    envs JSONB NULL
);


CREATE TYPE enum_session_status AS ENUM ('open', 'done');
CREATE TYPE enum_session_verb AS ENUM ('connect', 'exec');
CREATE TABLE sessions(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    
    connection VARCHAR(128) NOT NULL,
    connection_type enum_connection_type NOT NULL,
    input TEXT NULL,
    verb enum_session_verb NOT NULL,
    labels JSONB NULL,
    user_name VARCHAR(255) NULL,
    user_email VARCHAR(255) NULL,
    event_stream JSONB NULL,
    event_size int DEFAULT 0,
    status enum_session_status NOT NULL,
    metadata JSONB NULL, -- contains the redact count and other stuff

    created_at TIMESTAMP DEFAULT NOW(),
    ended_at TIMESTAMP NULL,

    UNIQUE(org_id, id)
);

CREATE TYPE enum_reviews_status AS ENUM ('PENDING', 'APPROVED', 'REVOKED', 'REJECTED', 'PROCESSING', 'EXECUTED', 'UNKNOWN');
CREATE TYPE enum_reviews_type AS ENUM ('onetime', 'jit');
CREATE TABLE reviews(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    -- check when deleting a particular connection
    session_id UUID NULL REFERENCES sessions (id),
    connection_id UUID NULL REFERENCES connections (id),
    
    type enum_reviews_type NOT NULL,
    
    connection_name VARCHAR(128) NOT NULL,
    input TEXT NULL,
    input_env_vars JSONB NULL,
    input_client_args TEXT[] NULL,
    access_duration BIGINT DEFAULT 0,
    status enum_reviews_status NOT NULL,
        
    created_at TIMESTAMP DEFAULT NOW(),
    revoked_at TIMESTAMP NULL
);

CREATE TABLE review_owners(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    review_id UUID NOT NULL REFERENCES reviews (id),

    email VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    slack_id VARCHAR(50) NULL
);

CREATE TABLE review_groups(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    review_id UUID NOT NULL REFERENCES reviews (id),
    review_owner_id UUID NOT NULL REFERENCES review_owners (id),

    group_name VARCHAR(100) NOT NULL,
    status enum_reviews_status NOT NULL,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE proxy_manager_state(
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    request_connection VARCHAR(128) NOT NULL,
    request_port VARCHAR(5) NOT NULL,
    request_access_duration bigint NOT NULL,
    metadata JSONB NULL,

    connected_at TIMESTAMP NULL
);

-- org
CREATE OR REPLACE VIEW public.orgs AS
    SELECT id, name, created_at FROM orgs;

-- users / login
CREATE OR REPLACE VIEW public.login AS
    SELECT id, redirect, outcome FROM login;

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

-- 
CREATE OR REPLACE FUNCTION public.update_groups(org_id UUID, user_id UUID, groups VARCHAR(100)[]) RETURNS SETOF public.user_groups_update AS $$
    DELETE FROM public.user_groups_update where user_id = user_id;
    WITH groups AS (
        SELECT org_id as org_id, user_id as user_id, UNNEST(groups) as name
    )
    INSERT INTO public.user_groups_update (org_id, user_id, name) 
    SELECT org_id, user_id, name FROM groups RETURNING *;
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
    SELECT id, org_id, agent_id, name, command, type, (SELECT envs FROM public.env_vars WHERE id = c.id) AS envs, created_at, updated_at
    FROM connections c;

CREATE OR REPLACE FUNCTION public.update_connection(org_id uuid, agent_id uuid, name text, command text[], type enum_connection_type, envs JSON) RETURNS SETOF public.connections ROWS 1 AS $$
    WITH p AS (
        SELECT 
            gen_random_uuid() as id, 
            org_id as org_id, 
            agent_id as agent_id, 
            name as name, 
            command as command, 
            type as type, 
            envs as envs
    ), conn AS (
        INSERT INTO connections (id, org_id, agent_id, name, command, type)
            (SELECT id, org_id, agent_id, name, command, type FROM p)
        ON CONFLICT (org_id, name)
            DO UPDATE SET agent_id = (SELECT agent_id FROM p), command = (SELECT command FROM p), updated_at = NOW()
        RETURNING *
    ), envs AS (
    INSERT INTO env_vars (id, org_id, envs) VALUES ((SELECT id FROM conn), (SELECT org_id FROM conn), (SELECT envs FROM p))
        ON CONFLICT (id)
        DO UPDATE SET envs = (SELECT envs FROM p)
        RETURNING *
    )
    SELECT c.id, c.org_id, c.agent_id, c.name, c.command, c.type, e.envs, c.created_at, c.updated_at
    FROM conn c
    INNER JOIN envs e
        ON e.id = c.id;
$$ LANGUAGE SQL;


GRANT webuser TO hoopadm;
GRANT usage ON SCHEMA public TO webuser;
GRANT usage ON SCHEMA private TO webuser;

GRANT INSERT, SELECT, UPDATE on public.login to webuser;
GRANT SELECT, INSERT, UPDATE ON public.users to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.users_update to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_groups_update to webuser;
-- GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_groups to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.connections to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.env_vars to webuser;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.agents to webuser;
GRANT SELECT, INSERT ON public.orgs to webuser;
