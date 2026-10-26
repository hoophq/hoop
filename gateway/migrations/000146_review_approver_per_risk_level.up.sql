BEGIN;

SET search_path TO private;

-- Who decides a review: human, or ai_agent for a review a sidecar's approval
-- agent filed already decided. Existing rows read as human.
ALTER TABLE reviews
    ADD COLUMN IF NOT EXISTS approver_type TEXT NOT NULL DEFAULT 'human'
        CONSTRAINT reviews_approver_type_check CHECK (approver_type IN ('human', 'ai_agent')),
    ADD COLUMN IF NOT EXISTS decision_reason TEXT NULL,
    ADD COLUMN IF NOT EXISTS risk_level TEXT NULL
        CONSTRAINT reviews_risk_level_check CHECK (risk_level IN ('high', 'medium', 'low'));

-- One live review per sidecar, listener, risk level and statement, for a
-- review filed under a risk level instead of an approval rule. The 000125
-- index skips these rows: their access_request_rule_name is NULL.
CREATE UNIQUE INDEX IF NOT EXISTS index_reviews_sidecar_level_statement
    ON reviews (org_id, sidecar_id, listener_name, risk_level, statement_hash)
    WHERE sidecar_id IS NOT NULL
      AND listener_name IS NOT NULL
      AND access_request_rule_name IS NULL
      AND risk_level IS NOT NULL
      AND statement_hash IS NOT NULL
      AND approver_type = 'human'
      AND status NOT IN ('EXECUTED', 'REJECTED', 'REVOKED');

-- A hold under the rule's own managed approval rule becomes a human level
-- that names the managed rule's groups. The managed rule stays: an older
-- sidecar files under it.
UPDATE ai_session_analyzer_rules AS r
SET sidecar_spec = (r.sidecar_spec - 'approval_rule')
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'high') = 'string'
             AND r.sidecar_spec ->> 'high' IN ('require_review', 'require_approval')
        THEN jsonb_build_object('high', lvl.obj) ELSE '{}'::jsonb END
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'medium') = 'string'
             AND r.sidecar_spec ->> 'medium' IN ('require_review', 'require_approval')
        THEN jsonb_build_object('medium', lvl.obj) ELSE '{}'::jsonb END
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'low') = 'string'
             AND r.sidecar_spec ->> 'low' IN ('require_review', 'require_approval')
        THEN jsonb_build_object('low', lvl.obj) ELSE '{}'::jsonb END
FROM access_request_rules AS a,
    LATERAL (SELECT jsonb_build_object('action', 'require_approval', 'approver', 'human')
        || CASE WHEN COALESCE(cardinality(a.reviewers_groups), 0) > 0
            THEN jsonb_build_object('approval_groups', to_jsonb(a.reviewers_groups))
            ELSE '{}'::jsonb END AS obj) AS lvl
WHERE a.org_id = r.org_id
  AND a.name = r.name
  AND a.managed_by = 'ai-session-analyzer'
  -- A human level grants one approval and no force approvers. A rule an admin
  -- edited to grant more or other keeps the bare hold, which files under it.
  AND NOT COALESCE(a.all_groups_must_approve, false)
  AND NOT EXISTS (SELECT 1 FROM unnest(a.force_approval_groups) AS f WHERE btrim(f) <> '')
  AND (a.min_approvals = 1 OR (a.min_approvals IS NULL
       AND (SELECT count(DISTINCT btrim(g)) FROM unnest(a.reviewers_groups) AS g WHERE btrim(g) <> '') = 1))
  AND jsonb_typeof(r.sidecar_spec) = 'object'
  AND r.sidecar_spec ->> 'approval_rule' = r.name
  AND (
        (jsonb_typeof(r.sidecar_spec -> 'high') = 'string'
            AND r.sidecar_spec ->> 'high' IN ('require_review', 'require_approval'))
     OR (jsonb_typeof(r.sidecar_spec -> 'medium') = 'string'
            AND r.sidecar_spec ->> 'medium' IN ('require_review', 'require_approval'))
     OR (jsonb_typeof(r.sidecar_spec -> 'low') = 'string'
            AND r.sidecar_spec ->> 'low' IN ('require_review', 'require_approval'))
  );

COMMIT;
