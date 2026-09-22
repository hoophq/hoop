BEGIN;

SET search_path TO private;

-- The SHA-256 of the exact statement bytes, in hex: what makes a retry
-- recognisable as the same statement. A review filed from a connection leaves
-- it NULL.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS statement_hash CHAR(64) NULL;

-- One live review per sidecar, listener, rule and statement. EXECUTED is
-- excluded so a consumed approval stops blocking and the next request waits for
-- a human again; every other status is covered, so racing first requests cannot
-- both file and a REJECTED row keeps denying.
--
-- The columns are required present rather than left to NULL distinctness, which
-- would index nothing and let duplicates through.
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_statement
    ON reviews (org_id, sidecar_id, listener_name, access_request_rule_name, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NOT NULL
      AND statement_hash IS NOT NULL
      AND status <> 'EXECUTED';

COMMIT;
