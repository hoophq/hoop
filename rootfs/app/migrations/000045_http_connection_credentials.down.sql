BEGIN;

ALTER TABLE private.serverconfig DROP COLUMN http_server_config;

COMMIT;