BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_security_audit_log_actor;
DROP INDEX IF EXISTS idx_security_audit_log_resource;
DROP INDEX IF EXISTS idx_security_audit_log_org_created;
DROP TABLE IF EXISTS security_audit_log;

COMMIT;
