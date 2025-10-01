BEGIN;

ALTER TABLE private.serverconfig ADD COLUMN rdp_server_config JSONB NULL;

COMMIT;
