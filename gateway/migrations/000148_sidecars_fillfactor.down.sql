BEGIN;

SET search_path TO private;

ALTER TABLE sidecars RESET (fillfactor, autovacuum_vacuum_scale_factor);

COMMIT;
