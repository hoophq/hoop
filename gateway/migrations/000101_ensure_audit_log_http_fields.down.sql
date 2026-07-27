BEGIN;

SET search_path TO private;

-- No-op: 000101 only re-applies (idempotently) the schema changes already
-- owned by 000064_audit_log_http_fields. Dropping the columns/indexes here
-- would revert 000064 as well, so this down migration intentionally does
-- nothing. Roll back 000064 to remove the HTTP audit-log fields.

COMMIT;
