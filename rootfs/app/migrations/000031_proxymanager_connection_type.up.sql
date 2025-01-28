BEGIN;

SET search_path TO private;
ALTER TABLE proxymanager_state ADD COLUMN connection_type VARCHAR(50) NULL;
ALTER TABLE proxymanager_state ADD COLUMN connection_subtype VARCHAR(50) NULL;

COMMIT;
