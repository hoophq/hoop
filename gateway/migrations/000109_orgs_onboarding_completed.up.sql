BEGIN;
SET search_path TO private;

-- Completion latch for the sidebar setup checklist (DEP-136). NULL means the
-- org is still onboarding. Never cleared: the individual steps are not
-- monotonic (an agent going offline unticks "Deploy Hoop Agent"), so completion
-- is recorded as a milestone instead of recomputed as live status.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMP NULL;

COMMIT;
