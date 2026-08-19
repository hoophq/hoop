BEGIN;
SET search_path TO private;

-- The index is owned by the table and goes with it.
DROP TABLE IF EXISTS ttfv_survey_responses;

COMMIT;
