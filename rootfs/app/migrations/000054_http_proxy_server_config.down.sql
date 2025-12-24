BEGIN;

ALTER TABLE private.serverconfig DROP COLUMN http_proxy_server_config;

COMMIT;
