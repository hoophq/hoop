BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS directory_groups;
DROP TABLE IF EXISTS directory_users;
DROP TABLE IF EXISTS directory_sync_configs;

COMMIT;
