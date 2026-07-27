BEGIN;

SET search_path TO private;

-- Idempotent re-apply of 000064_audit_log_http_fields for environments
-- where the original migration was recorded but not (fully) applied.

-- Remove redundant columns (resource_id and resource_name will be in the payload)
ALTER TABLE security_audit_log 
  DROP COLUMN IF EXISTS resource_id,
  DROP COLUMN IF EXISTS resource_name;

-- Add HTTP request/response fields
ALTER TABLE security_audit_log 
  ADD COLUMN IF NOT EXISTS http_method VARCHAR(16) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS http_status INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS http_path VARCHAR(512) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS client_ip VARCHAR(45) NOT NULL DEFAULT '';

-- Create indexes for common filter queries
CREATE INDEX IF NOT EXISTS idx_security_audit_log_http_status ON security_audit_log(org_id, http_status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_audit_log_client_ip ON security_audit_log(org_id, client_ip, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_audit_log_http_path ON security_audit_log(org_id, http_path, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_security_audit_log_http_method ON security_audit_log(org_id, http_method, created_at DESC);

COMMIT;
