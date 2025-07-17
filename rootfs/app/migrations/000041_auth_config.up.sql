BEGIN;

SET search_path TO private;

CREATE TYPE enum_auth_method AS ENUM ('local', 'oidc', 'saml');
CREATE TABLE authconfig(
    org_id UUID NOT NULL REFERENCES orgs (id),

    auth_method enum_auth_method NULL,
    oidc_config JSONB NULL,
    saml_config JSONB NULL,

    api_key VARCHAR(255) NULL,
    rollout_api_key VARCHAR(255) NULL,

    webapp_users_management enum_generic_toggle_status NULL,
    admin_role_name VARCHAR(128) NULL,
    auditor_role_name VARCHAR(128) NULL,

    updated_at TIMESTAMP DEFAULT NOW()
);

ALTER TABLE private.serverconfig DROP COLUMN webapp_users_management;

COMMIT;
