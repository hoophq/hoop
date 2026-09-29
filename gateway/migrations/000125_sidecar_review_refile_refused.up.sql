BEGIN;

SET search_path TO private;

-- A REJECTED or REVOKED review no longer blocks the same statement. A resend
-- files a new review, the same as after EXECUTED.
DROP INDEX IF EXISTS index_reviews_sidecar_statement;
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_statement
    ON reviews (org_id, sidecar_id, listener_name, access_request_rule_name, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NOT NULL
      AND statement_hash IS NOT NULL
      AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED');

COMMIT;
