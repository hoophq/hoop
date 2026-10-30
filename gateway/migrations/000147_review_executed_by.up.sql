BEGIN;

SET search_path TO private;

-- executed_by: the email of the user whose run moved a one-time review from
-- APPROVED to PROCESSING. NULL on older rows and on a run with no email.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS executed_by VARCHAR(255) NULL;

COMMIT;
