BEGIN;

SET search_path TO private;

CREATE TABLE security_audit_log (
    id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),

    -- Who
    actor_subject VARCHAR(255) NOT NULL,
    actor_email   VARCHAR(255) NULL,
    actor_name    VARCHAR(255) NULL,

    -- When
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- What
    resource_type  VARCHAR(64) NOT NULL,
    action         VARCHAR(32) NOT NULL,
    resource_id    UUID NULL,
    resource_name  VARCHAR(255) NULL,

    -- Details (request payload with secrets redacted)
    request_payload_redacted JSONB NULL,

    -- Outcome (true = success, false = failure)
    outcome       BOOLEAN NOT NULL,
    error_message TEXT NULL
);

CREATE INDEX idx_security_audit_log_org_created ON security_audit_log (org_id, created_at DESC);
CREATE INDEX idx_security_audit_log_resource ON security_audit_log (org_id, resource_type, created_at DESC);
CREATE INDEX idx_security_audit_log_actor ON security_audit_log (org_id, actor_subject, created_at DESC);

COMMIT;
