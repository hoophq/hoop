BEGIN;

SET search_path TO private;

CREATE TYPE enum_generic_toggle_status AS ENUM ('active', 'inactive');
CREATE TABLE serverconfig(
    product_analytics enum_generic_toggle_status NULL,
    webapp_users_management enum_generic_toggle_status NULL,
    grpc_server_url VARCHAR(255) NULL,
    shared_signing_key TEXT NULL,

    updated_at TIMESTAMP DEFAULT NOW()
);

COMMIT;
