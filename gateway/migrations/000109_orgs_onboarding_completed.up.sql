BEGIN;
SET search_path TO private;

-- Latches for the sidebar setup checklist (DEP-136). The checks behind it are
-- not monotonic (an agent going offline unticks "Deploy Hoop Agent"), so
-- progress is recorded as milestones. Neither latch is ever cleared.

-- {"<step_key>": "<RFC3339 first completion>"} — presence is what counts.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS onboarding_steps JSONB NOT NULL DEFAULT '{}';

-- NULL means the org is still onboarding.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMP NULL;

COMMIT;
