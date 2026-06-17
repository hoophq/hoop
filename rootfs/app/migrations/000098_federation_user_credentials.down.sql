BEGIN;
SET search_path TO private;

DROP TABLE IF EXISTS federation_oauth_states;
DROP TABLE IF EXISTS federation_user_credentials;

COMMIT;
