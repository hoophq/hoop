BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS datamasking_connection_associations;
DROP TABLE IF EXISTS datamasking;
DROP TYPE enum_datamasking_assoc_status;

COMMIT;