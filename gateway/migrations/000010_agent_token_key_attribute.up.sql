BEGIN;

SET search_path TO private;

DROP VIEW public.agents;
ALTER TYPE enum_agent_mode ADD VALUE IF NOT EXISTS 'multi-connection';
ALTER TABLE agents ADD COLUMN key VARCHAR(255) NULL;
ALTER TABLE agents RENAME COLUMN token TO key_hash;

CREATE VIEW public.agents AS
    SELECT
        id, org_id, name, mode, key, key_hash, metadata, status, created_at, updated_at
    FROM agents;

COMMIT;