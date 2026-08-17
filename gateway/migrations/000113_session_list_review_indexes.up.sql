-- Indexes for the review side of the session list query.
--
-- index_review_groups_review_id
--   The page query builds each row's review JSON with a correlated jsonb_agg
--   over review_groups keyed by review_id. review_groups had no index on that
--   column, so every rendered review cost a sequential scan of the whole
--   table: a 1M-session load test recorded 14469 seq scans and 0 index scans.
--
-- index_reviews_org_status
--   Supports resolving a page directly from reviews when review.status is
--   filtered, instead of walking sessions in created_at order looking for the
--   few (or zero) rows that match.
--
--   Indexed on the enum column itself, not on (status::text): casting an enum
--   to text is only STABLE, because ALTER TYPE ... RENAME VALUE can change the
--   result, so Postgres rejects the expression index outright ("functions in
--   index expression must be marked IMMUTABLE"). To use this index the query
--   therefore has to compare against the enum, and casting a bind parameter to
--   an enum raises "invalid input value for enum" for a value that is not a
--   label. ListSessions handles that by only emitting the enum comparison for a
--   status it recognises (models.IsValidReviewStatus) and falling back to the
--   text LIKE form otherwise, so an unknown filter value keeps returning an
--   empty page rather than a 500.
--
-- Deliberately plain, blocking CREATE INDEXes rather than CONCURRENTLY.
-- golang-migrate's postgres driver Execs each file as one statement batch, so a
-- CONCURRENTLY build would have to be its own single-statement file outside
-- this BEGIN/COMMIT. That is possible, but a CONCURRENTLY build that fails
-- leaves an INVALID index AND a dirty schema_migrations row, and the gateway
-- refuses to start on a dirty database (models/bootstrap/bootstrap.go). Both
-- tables here are small relative to sessions — one review per reviewed session,
-- a couple of review_groups rows per review — so the SHARE lock is short and
-- only blocks review writes, which are rare. That trade is worth far more than
-- avoiding it at the cost of a failure mode that bricks gateway startup.
BEGIN;
SET search_path TO private;

CREATE INDEX IF NOT EXISTS index_review_groups_review_id
    ON review_groups (review_id);

CREATE INDEX IF NOT EXISTS index_reviews_org_status
    ON reviews (org_id, status);

COMMIT;
