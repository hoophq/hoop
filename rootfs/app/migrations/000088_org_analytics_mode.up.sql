BEGIN;
SET search_path TO private;

-- Analytics defaults to identified so new installations are addressable by
-- default. Admins explicitly opt their org out by switching to anonymous
-- or disabled from Settings -> Organization.
ALTER TABLE orgs ADD COLUMN IF NOT EXISTS analytics_mode TEXT NOT NULL DEFAULT 'identified';
ALTER TABLE orgs ALTER COLUMN analytics_mode SET DEFAULT 'identified';

ALTER TABLE orgs DROP CONSTRAINT IF EXISTS orgs_analytics_mode_check;
ALTER TABLE orgs ADD CONSTRAINT orgs_analytics_mode_check
  CHECK (analytics_mode IN ('identified', 'anonymous', 'disabled'));

-- Backfill existing orgs to match their effective M1 behavior:
--   * Global kill-switch was off => disabled.
--   * Enterprise license         => anonymous (was hashed-only under M1).
--   * Otherwise (OSS or unset)   => identified (was sending email/name under M1).
UPDATE orgs o SET analytics_mode = CASE
    WHEN EXISTS (SELECT 1 FROM serverconfig WHERE product_analytics = 'inactive') THEN 'disabled'
    WHEN COALESCE(o.license_data->'payload'->>'type', 'oss') = 'enterprise' THEN 'anonymous'
    ELSE 'identified'
END;

ALTER TABLE serverconfig DROP COLUMN IF EXISTS product_analytics;

COMMIT;
