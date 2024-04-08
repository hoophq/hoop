BEGIN;

SET search_path TO private;

DROP VIEW public.agents;
ALTER TABLE agents DROP COLUMN key;
ALTER TABLE agents RENAME COLUMN key_hash TO token;

CREATE VIEW public.agents AS
    SELECT
        id, org_id, name, mode, token, metadata, status, created_at, updated_at
    FROM agents;

COMMIT;