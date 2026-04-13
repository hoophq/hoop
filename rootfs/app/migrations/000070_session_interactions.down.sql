DROP TABLE IF EXISTS private.session_interactions;
ALTER TABLE private.sessions DROP COLUMN IF EXISTS type;
DROP TYPE IF EXISTS enum_session_type;