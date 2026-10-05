BEGIN;

SET search_path TO private;

DROP TABLE IF EXISTS slack_review_settlements;
DROP TABLE IF EXISTS slack_review_messages;

COMMIT;
