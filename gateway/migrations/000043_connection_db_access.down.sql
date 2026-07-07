BEGIN;

DROP TABLE private.connection_dbaccess;
ALTER TABLE private.serverconfig DROP COLUMN postgres_server_config;

COMMIT;