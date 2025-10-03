BEGIN;

SET search_path TO private;

ALTER TABLE connections
    DROP CONSTRAINT connection_resource_fk,
    DROP COLUMN resource_name;

DROP TABLE resources;

COMMIT;