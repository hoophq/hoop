BEGIN;

SET search_path TO private;

-- Drop the new indexes
DROP INDEX IF EXISTS idx_security_audit_log_http_method;
DROP INDEX IF EXISTS idx_security_audit_log_http_path;
DROP INDEX IF EXISTS idx_security_audit_log_client_ip;
DROP INDEX IF EXISTS idx_security_audit_log_http_status;

-- Remove HTTP fields
ALTER TABLE security_audit_log 
  DROP COLUMN IF EXISTS client_ip,
  DROP COLUMN IF EXISTS http_path,
  DROP COLUMN IF EXISTS http_status,
  DROP COLUMN IF EXISTS http_method;

-- Restore old columns
ALTER TABLE security_audit_log 
  ADD COLUMN resource_id UUID NULL,
  ADD COLUMN resource_name VARCHAR(255) NULL;

COMMIT;
