-- Idempotent / re-runnable: every statement uses IF EXISTS / IF NOT EXISTS or
-- the drop-before-add pattern, so running this migration twice is a no-op on
-- the second pass rather than an error.
BEGIN;
SET search_path TO private;

-- The "readonly" fallback policy and its readonly_principal target are being
-- replaced by the "static" policy (skip federation, keep the connection's
-- existing static credentials). Coerce any existing readonly rows to the
-- secure default before dropping support so no row violates the new check.
UPDATE connection_federation_configs
  SET fallback_policy = 'deny'
  WHERE fallback_policy = 'readonly';

-- Drop the readonly-specific guard and column.
ALTER TABLE connection_federation_configs
  DROP CONSTRAINT IF EXISTS chk_readonly_principal_present;
ALTER TABLE connection_federation_configs
  DROP COLUMN IF EXISTS readonly_principal;

-- Swap the fallback_policy allow-list from (deny, readonly) to (deny, static).
ALTER TABLE connection_federation_configs
  DROP CONSTRAINT IF EXISTS connection_federation_configs_fallback_policy_check;
ALTER TABLE connection_federation_configs
  ADD CONSTRAINT connection_federation_configs_fallback_policy_check
  CHECK (fallback_policy IN ('deny', 'static'));

COMMIT;
