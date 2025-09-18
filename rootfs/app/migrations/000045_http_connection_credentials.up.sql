BEGIN;

ALTER TABLE private.serverconfig ADD COLUMN http_server_config JSONB NULL DEFAULT '{"listen_address": "localhost:8080"}';

COMMIT;