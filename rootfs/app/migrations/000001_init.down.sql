BEGIN;

DROP TABLE private.login CASCADE;
DROP TABLE private.users CASCADE;
DROP TABLE private.service_accounts CASCADE;
DROP TABLE private.user_groups CASCADE;
DROP TABLE private.agents CASCADE;
DROP TABLE private.clientkeys CASCADE;
DROP TABLE private.connections CASCADE;
DROP TABLE private.plugins CASCADE;
DROP TABLE private.plugin_connections CASCADE;
DROP TABLE private.env_vars CASCADE;
DROP TABLE private.sessions CASCADE;
DROP TABLE private.blobs CASCADE;
DROP TABLE private.reviews CASCADE;
DROP TABLE private.review_groups CASCADE;
DROP TABLE private.proxymanager_state CASCADE;
DROP TABLE private.orgs CASCADE;

DROP TYPE private.enum_user_status;
DROP TYPE private.enum_service_account_status;
DROP TYPE private.enum_agent_mode;
DROP TYPE private.enum_agent_status;
DROP TYPE private.enum_clientkeys_status;
DROP TYPE private.enum_connection_type;
DROP TYPE private.enum_session_status;
DROP TYPE private.enum_session_verb;
DROP TYPE private.enum_blob_type;
DROP TYPE private.enum_reviews_status;
DROP TYPE private.enum_reviews_type;
DROP TYPE private.enum_proxymanager_status;

DROP SCHEMA private CASCADE;

COMMIT;