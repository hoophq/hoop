BEGIN;

SET search_path TO private;

-- The rule as the SIDECAR spells it.
--
-- The gateway and the sidecar are two implementations of the same three
-- feature names, and their vocabularies do not meet. The gateway masks through
-- a DLP provider with entity groups, custom recognizers and a score threshold;
-- a sidecar masks a decoded response frame with entities OR column names, a
-- strategy (redact, mask, partial, hash) and a keep_last. The gateway knows two
-- guardrail rule types; a sidecar knows seven, plus an `operations` scope on
-- every one of them and `action: defer` to hand the verdict to Rego. The
-- gateway's analyzer answers with allow_execution / block_execution /
-- require_access_request; a sidecar's answers with allow / warn / block /
-- defer, and carries the trigger and the call budget beside them.
--
-- None of that fits the typed columns beside it, and none of it should:
-- those columns are the gateway's own feature and must keep working exactly as
-- they do. So the sidecar's rule is stored whole, in its own vocabulary, in
-- one column that the gateway's code never reads.
--
-- The value is the BLOCK the listener receives, not a translation of it:
-- {"rules": [...]} for guardrails and masking, and the analyzer block itself
-- for the analyzer. Composition then places it rather than converting it,
-- which is what removes the whole class of bug where a rule saves cleanly and
-- reaches a sidecar meaning something else.
--
-- NULL means "this rule is not for a sidecar", which is every rule a gateway
-- writes. Only the control plane fills it.

ALTER TABLE guardrail_rules
    ADD COLUMN IF NOT EXISTS sidecar_spec JSONB NULL;

ALTER TABLE datamasking_rules
    ADD COLUMN IF NOT EXISTS sidecar_spec JSONB NULL;

ALTER TABLE ai_session_analyzer_rules
    ADD COLUMN IF NOT EXISTS sidecar_spec JSONB NULL;

COMMIT;
