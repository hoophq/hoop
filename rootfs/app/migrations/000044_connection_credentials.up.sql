BEGIN;

SET search_path TO private;

ALTER TABLE connection_dbaccess RENAME TO connection_credentials;
ALTER TABLE serverconfig ADD COLUMN ssh_server_config JSONB NULL;

ALTER TABLE connection_credentials ADD COLUMN connection_type VARCHAR(64) NULL;

COMMIT;