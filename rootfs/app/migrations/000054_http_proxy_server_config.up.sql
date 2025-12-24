BEGIN;

ALTER TABLE private.serverconfig ADD COLUMN http_proxy_server_config JSONB DEFAULT '{"listen_address":"0.0.0.0:18888"}';

COMMIT;
