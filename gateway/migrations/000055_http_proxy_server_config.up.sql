BEGIN;

ALTER TABLE private.serverconfig 
ADD COLUMN IF NOT EXISTS http_proxy_server_config JSONB DEFAULT '{"listen_address":"0.0.0.0:18888"}';

COMMIT;
