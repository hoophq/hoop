BEGIN;

SET search_path TO private;

ALTER TYPE enum_user_status ADD VALUE IF NOT EXISTS 'invited';

COMMIT;

