BEGIN;
SET search_path TO private;

-- Latches for the sidebar setup checklist (DEP-136). The checks behind it are
-- not monotonic — an agent going offline unticks "Deploy Hoop Agent", deleting
-- a rule unticks "Explore AI Data Masking" — so progress is recorded as
-- milestones instead of recomputed as live status. Neither latch is cleared.

-- Per step: {"<step_key>": "<RFC3339 first completion>"}. A key being present
-- is what counts; the timestamp is for support and analytics.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS onboarding_steps JSONB NOT NULL DEFAULT '{}';

-- Whole checklist. NULL means the org is still onboarding.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMP NULL;

COMMIT;
