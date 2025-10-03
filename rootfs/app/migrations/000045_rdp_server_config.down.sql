BEGIN;

ALTER TABLE private.serverconfig DROP COLUMN rdp_server_config;

COMMIT;
