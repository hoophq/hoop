BEGIN;

SET search_path TO private;

ALTER TABLE connections
    DROP CONSTRAINT fk_connection_resource,
    DROP COLUMN resource_name;

DROP TABLE resources;

COMMIT;