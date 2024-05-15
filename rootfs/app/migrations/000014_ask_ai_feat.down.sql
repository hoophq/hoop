BEGIN;

SET search_path TO private;

DROP VIEW IF EXISTS public.audit;
DROP TABLE IF EXISTS audit;

COMMIT;