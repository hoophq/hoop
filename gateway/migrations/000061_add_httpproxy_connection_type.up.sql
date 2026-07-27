BEGIN;

ALTER TYPE private.enum_connection_type ADD VALUE IF NOT EXISTS 'httpproxy';

COMMIT;
