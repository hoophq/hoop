BEGIN;

SET search_path TO private;

-- Every connection a sidecar manages, not only the backfilled ones: without
-- the projection none of them is kept in step, and 000128 down drops the
-- columns that say which listener each one was.
CREATE TEMP TABLE _gone ON COMMIT DROP AS
SELECT org_id, resource_name FROM connections WHERE sidecar_id IS NOT NULL;

DELETE FROM connections WHERE sidecar_id IS NOT NULL;

DELETE FROM resources r
USING _gone g
WHERE r.org_id = g.org_id AND r.name = g.resource_name
  AND NOT EXISTS (SELECT 1 FROM connections c WHERE c.org_id = r.org_id AND c.resource_name = r.name);

COMMIT;
