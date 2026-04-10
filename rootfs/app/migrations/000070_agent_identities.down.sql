BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS agent_identity_secrets;
DROP TABLE IF EXISTS agent_identities;

COMMIT;
