BEGIN;

SET search_path TO private;

DROP VIEW public.orgs;
ALTER TABLE private.orgs DROP COLUMN license;
CREATE VIEW public.orgs AS SELECT id, name, created_at FROM orgs;

COMMIT;