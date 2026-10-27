BEGIN;

SET search_path TO private;

-- Every sidecar heartbeat loads the live credentials of its lanes by
-- connection_name prefix. text_pattern_ops serves the LIKE 'sidecar:<name>/%'.
CREATE INDEX IF NOT EXISTS idx_conn_cred_sidecar_lane
    ON connection_credentials (org_id, connection_name text_pattern_ops)
    WHERE connection_type = 'sidecar' AND revoked_at IS NULL;

COMMIT;
