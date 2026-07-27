BEGIN;

SET search_path TO private;

CREATE TABLE connection_dbaccess(
    id uuid DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    user_subject VARCHAR(255) NOT NULL,
    connection_name VARCHAR(128) NOT NULL,
    secret_key_hash VARCHAR(128) NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    expire_at TIMESTAMP NOT NULL
);

ALTER TABLE private.serverconfig ADD COLUMN postgres_server_config JSONB NULL;

COMMIT;
