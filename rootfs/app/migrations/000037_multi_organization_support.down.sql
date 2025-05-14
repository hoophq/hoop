BEGIN;

DROP VIEW IF EXISTS public.user_preferences;
DROP VIEW IF EXISTS public.user_organizations;
DROP TABLE IF EXISTS private.user_preferences;
DROP TABLE IF EXISTS private.user_organizations;

COMMIT;
