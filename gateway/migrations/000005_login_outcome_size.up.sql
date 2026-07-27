BEGIN;

SET search_path TO private;

DROP VIEW IF EXISTS public.login;
ALTER TABLE private.login ALTER COLUMN outcome TYPE VARCHAR(200);
ALTER TABLE private.login ADD COLUMN updated_at TIMESTAMP DEFAULT NOW();
CREATE VIEW public.login AS
    SELECT id, redirect, slack_id, outcome, updated_at, created_at FROM login;

COMMIT;