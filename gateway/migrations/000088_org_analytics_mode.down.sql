BEGIN;
SET search_path TO private;

ALTER TABLE serverconfig ADD COLUMN IF NOT EXISTS product_analytics private.enum_generic_toggle_status;

-- Best-effort reverse: any org currently disabled implies the old global
-- kill-switch was previously off.
UPDATE serverconfig SET product_analytics = 'inactive'
WHERE EXISTS (SELECT 1 FROM orgs WHERE analytics_mode = 'disabled');

ALTER TABLE orgs DROP CONSTRAINT IF EXISTS orgs_analytics_mode_check;
ALTER TABLE orgs DROP COLUMN IF EXISTS analytics_mode;

COMMIT;
