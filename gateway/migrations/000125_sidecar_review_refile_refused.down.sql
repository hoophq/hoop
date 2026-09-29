BEGIN;

SET search_path TO private;

-- The old index allows one non-EXECUTED row per statement. Keep the hash on the
-- newest row and clear it on older ones, so no review row is deleted.
UPDATE reviews SET statement_hash = NULL
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY org_id, sidecar_id, listener_name, access_request_rule_name, statement_hash
            ORDER BY created_at DESC, id DESC) AS rn
        FROM reviews
        WHERE sidecar_id IS NOT NULL
          AND listener_name IS NOT NULL
          AND access_request_rule_name IS NOT NULL
          AND statement_hash IS NOT NULL
          AND status <> 'EXECUTED'
    ) ranked
    WHERE rn > 1
);

DROP INDEX IF EXISTS index_reviews_sidecar_statement;
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_statement
    ON reviews (org_id, sidecar_id, listener_name, access_request_rule_name, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NOT NULL
      AND statement_hash IS NOT NULL
      AND status <> 'EXECUTED';

COMMIT;
