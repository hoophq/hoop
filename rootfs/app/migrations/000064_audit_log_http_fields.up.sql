BEGIN;

SET search_path TO private;

-- Remove redundant columns (resource_id and resource_name will be in the payload)
ALTER TABLE security_audit_log 
  DROP COLUMN IF EXISTS resource_id,
  DROP COLUMN IF EXISTS resource_name;

-- Add HTTP request/response fields
ALTER TABLE security_audit_log 
  ADD COLUMN http_method VARCHAR(16) NOT NULL DEFAULT '',
  ADD COLUMN http_status INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN http_path VARCHAR(512) NOT NULL DEFAULT '',
  ADD COLUMN client_ip VARCHAR(45) NOT NULL DEFAULT '';

-- Create indexes for common filter queries
CREATE INDEX idx_security_audit_log_http_status ON security_audit_log(org_id, http_status, created_at DESC);
CREATE INDEX idx_security_audit_log_client_ip ON security_audit_log(org_id, client_ip, created_at DESC);
CREATE INDEX idx_security_audit_log_http_path ON security_audit_log(org_id, http_path, created_at DESC);
CREATE INDEX idx_security_audit_log_http_method ON security_audit_log(org_id, http_method, created_at DESC);

COMMIT;
