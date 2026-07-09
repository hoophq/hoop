BEGIN;

SET search_path TO private;

-- beginning from this version, the migrations files
-- will only perform the migration of the private schema
-- the state of the views and functions will be handled
-- by another process.
DROP function public.blob_input(public.reviews);
DROP function public.blob_input(public.sessions);
DROP function public.blob_stream(public.sessions);
DROP function public.env_vars(public.plugin_connections);
DROP function public.env_vars(public.plugins);
DROP function public.groups(public.serviceaccounts);
DROP function public.groups(public.users);
DROP function public.update_connection(json);
DROP function public.update_serviceaccounts(uuid,uuid,text,text,private.enum_service_account_status,character varying[]);
DROP function public.update_users(json);
DROP view public.agents;
DROP view public.audit;
DROP view public.blobs;
DROP view public.clientkeys;
DROP view public.connections;
DROP view public.env_vars;
DROP view public.login;
DROP view public.orgs;
DROP view public.plugin_connections;
DROP view public.plugins;
DROP view public.proxymanager_state;
DROP view public.review_groups;
DROP view public.reviews;
DROP view public.serviceaccounts;
DROP view public.sessions;
DROP view public.user_groups;
DROP view public.users;

CREATE TABLE appstate(
    id SERIAL PRIMARY KEY,

    -- <resource-type> <resource> content
    -- to perform the rollback
    state_rollback TEXT NOT NULL,
    -- checksum of the current state
    checksum VARCHAR(128) NOT NULL,
    role_name VARCHAR(255) NOT NULL,
    schema VARCHAR(100) NOT NULL,

    version VARCHAR(255) NULL,
    commit VARCHAR(128) NULL,
    pgversion VARCHAR(255) NULL,

    created_at TIMESTAMP DEFAULT NOW()
);

CREATE SCHEMA public_b;

COMMIT;