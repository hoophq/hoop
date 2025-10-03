BEGIN;

ALTER TABLE private.serverconfig ADD COLUMN rdp_server_config JSONB DEFAULT '{"listen_address":"0.0.0.0:13389"}';


COMMIT;
