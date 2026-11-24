BEGIN;

SET search_path TO private;

-- The AI routing decision of a review (ENG-663): groups, notified users,
-- channels, reason and fallback. Null on a review with static routing.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS routing JSONB;

COMMIT;
