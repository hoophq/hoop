BEGIN;
SET search_path TO private;

-- Restore the readonly_principal column (nullable; rolled-back rows have no
-- value to recover, which is acceptable for a downgrade).
ALTER TABLE connection_federation_configs
  ADD COLUMN IF NOT EXISTS readonly_principal TEXT;

-- Coerce any "static" rows back to the secure default before restoring the
-- old allow-list, otherwise the re-added check would reject them.
UPDATE connection_federation_configs
  SET fallback_policy = 'deny'
  WHERE fallback_policy = 'static';

ALTER TABLE connection_federation_configs
  DROP CONSTRAINT IF EXISTS connection_federation_configs_fallback_policy_check;
ALTER TABLE connection_federation_configs
  ADD CONSTRAINT connection_federation_configs_fallback_policy_check
  CHECK (fallback_policy IN ('deny', 'readonly'));

ALTER TABLE connection_federation_configs
  ADD CONSTRAINT chk_readonly_principal_present
  CHECK (fallback_policy <> 'readonly' OR readonly_principal IS NOT NULL);

COMMIT;
