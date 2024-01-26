BEGIN;

SET search_path TO private;

DROP VIEW IF EXISTS public.login;
ALTER TABLE private.login ALTER COLUMN outcome TYPE VARCHAR(50);
ALTER TABLE private.login DROP COLUMN updated_at;
ALTER TABLE private.login DROP COLUMN created_at;
CREATE VIEW public.login AS
    SELECT id, redirect, slack_id, outcome FROM login;

COMMIT;