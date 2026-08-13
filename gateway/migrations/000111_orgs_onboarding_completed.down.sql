BEGIN;
SET search_path TO private;

ALTER TABLE orgs DROP COLUMN IF EXISTS onboarding_steps;
ALTER TABLE orgs DROP COLUMN IF EXISTS onboarding_completed_at;

COMMIT;
