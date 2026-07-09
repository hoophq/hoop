BEGIN;

SET search_path TO private;

DROP VIEW public.agents;
ALTER TABLE agents ALTER COLUMN name TYPE VARCHAR(63);
CREATE VIEW public.agents AS
    SELECT
        id, org_id, name, mode, token, metadata, status, created_at, updated_at
    FROM agents;

COMMIT;