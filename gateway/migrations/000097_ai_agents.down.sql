BEGIN;

SET search_path TO private;

DROP INDEX IF EXISTS idx_user_groups_ai_agent_name;
ALTER TABLE user_groups DROP COLUMN IF EXISTS ai_agent_id;
DROP TABLE IF EXISTS ai_agents;

COMMIT;