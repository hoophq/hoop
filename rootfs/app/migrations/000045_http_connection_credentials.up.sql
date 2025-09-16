BEGIN;

ALTER TABLE private.serverconfig ADD COLUMN http_server_config JSONB NULL;

COMMIT;