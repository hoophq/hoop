BEGIN;

ALTER TABLE private.serverconfig ADD COLUMN rdp_server_config JSONB NULL;
ALTER TYPE private.enum_connection_type ADD VALUE IF NOT EXISTS 'rdp';

COMMIT;
