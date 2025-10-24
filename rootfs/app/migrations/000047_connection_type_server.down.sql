BEGIN;

ALTER TYPE private.enum_connection_type DROP VALUE 'server';

COMMIT;
