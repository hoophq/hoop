BEGIN;

SET search_path TO private;

DROP VIEW public.orgs;
ALTER TABLE private.orgs ADD COLUMN license VARCHAR(64) NULL;
CREATE VIEW public.orgs AS SELECT id, name, license, created_at FROM orgs;

COMMIT;