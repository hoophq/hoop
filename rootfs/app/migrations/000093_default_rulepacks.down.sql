-- Remove the managed default rulepacks (and everything that hangs off them)
-- seeded by 000093_default_rulepacks.up.sql, across all organizations.
--
-- We identify the seeded rulepacks by their stable display_name + is_managed
-- + version triple, which is what the up migration writes. Anything a user
-- has manually created with a different display_name is left untouched.
--
-- Cascading FKs handle the rest:
--   * guardrail_rules.rulepack_id -> ON DELETE CASCADE
--   * attributes.rulepack_id      -> ON DELETE CASCADE
--   * guardrail_rules_attributes  -> FKs ON DELETE CASCADE from both sides

BEGIN;

SET search_path TO private;

DELETE FROM rulepacks
    WHERE is_managed = true;

COMMIT;
