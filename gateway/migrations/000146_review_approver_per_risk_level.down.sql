BEGIN;

SET search_path TO private;

-- A human level goes back to the bare hold under the rule's own managed
-- approval rule, only when that changes no approver: every human level names
-- the same groups, the managed rule carries exactly them, no level is held by
-- the approval agent (approver ai_agent, or a hold object with no approver,
-- which defaults to it), and no bare hold files under another rule. A level
-- with no groups means the administrators, and the admin group name is
-- configuration this migration cannot read, so such a spec is not converted.
-- Any other spec stays in the object form, and the older binary refuses to
-- serve it.
WITH levels AS (
    SELECT r.org_id, r.name, r.sidecar_spec -> k.lvl AS v, r.sidecar_spec ->> 'approval_rule' AS approval_rule
    FROM ai_session_analyzer_rules AS r
    CROSS JOIN (VALUES ('high'), ('medium'), ('low')) AS k (lvl)
    WHERE jsonb_typeof(r.sidecar_spec) = 'object'
),
human AS (
    SELECT org_id, name,
        ARRAY(SELECT DISTINCT btrim(g)
              FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(v -> 'approval_groups') = 'array'
                  THEN v -> 'approval_groups' ELSE '[]'::jsonb END) AS g
              WHERE btrim(g) <> '' ORDER BY 1) AS grp
    FROM levels
    WHERE jsonb_typeof(v) = 'object'
      AND v ->> 'approver' = 'human'
      AND v ->> 'action' IN ('require_review', 'require_approval')
),
convertible AS (
    SELECT h.org_id, h.name
    FROM human AS h
    JOIN access_request_rules AS a
      ON a.org_id = h.org_id AND a.name = h.name AND a.managed_by = 'ai-session-analyzer'
     -- The rule grants what a human level grants: one approval, no force
     -- approvers. An admin may have edited it through the access request API.
     AND NOT COALESCE(a.all_groups_must_approve, false)
     AND NOT EXISTS (SELECT 1 FROM unnest(a.force_approval_groups) AS f WHERE btrim(f) <> '')
     AND (a.min_approvals = 1 OR (a.min_approvals IS NULL
          AND (SELECT count(DISTINCT btrim(g)) FROM unnest(a.reviewers_groups) AS g WHERE btrim(g) <> '') = 1))
    GROUP BY h.org_id, h.name, a.reviewers_groups
    HAVING COUNT(DISTINCT h.grp) = 1
       AND cardinality(MIN(h.grp)) > 0
       AND MIN(h.grp) = ARRAY(SELECT DISTINCT btrim(g) FROM unnest(a.reviewers_groups) AS g
                              WHERE btrim(g) <> '' ORDER BY 1)
)
UPDATE ai_session_analyzer_rules AS r
SET sidecar_spec = r.sidecar_spec
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'high') = 'object'
             AND r.sidecar_spec -> 'high' ->> 'approver' = 'human'
             AND r.sidecar_spec -> 'high' ->> 'action' IN ('require_review', 'require_approval')
        THEN '{"high": "require_review"}'::jsonb ELSE '{}'::jsonb END
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'medium') = 'object'
             AND r.sidecar_spec -> 'medium' ->> 'approver' = 'human'
             AND r.sidecar_spec -> 'medium' ->> 'action' IN ('require_review', 'require_approval')
        THEN '{"medium": "require_review"}'::jsonb ELSE '{}'::jsonb END
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'low') = 'object'
             AND r.sidecar_spec -> 'low' ->> 'approver' = 'human'
             AND r.sidecar_spec -> 'low' ->> 'action' IN ('require_review', 'require_approval')
        THEN '{"low": "require_review"}'::jsonb ELSE '{}'::jsonb END
    || jsonb_build_object('approval_rule', r.name)
FROM convertible AS c
WHERE c.org_id = r.org_id
  AND c.name = r.name
  AND NOT EXISTS (
        SELECT 1 FROM levels AS l
        WHERE l.org_id = r.org_id AND l.name = r.name
          AND (
                (jsonb_typeof(l.v) = 'string'
                    AND l.v #>> '{}' IN ('require_review', 'require_approval')
                    AND COALESCE(l.approval_rule, '') <> r.name)
             OR (jsonb_typeof(l.v) = 'object'
                    AND l.v ->> 'action' IN ('require_review', 'require_approval')
                    AND COALESCE(l.v ->> 'approver', '') <> 'human')
          )
  );

-- An object that holds only a non-hold action is that action. A hold object
-- with no approver is the approval agent's and stays.
UPDATE ai_session_analyzer_rules AS r
SET sidecar_spec = r.sidecar_spec
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'high') = 'object' AND (r.sidecar_spec -> 'high') - 'action' = '{}'::jsonb
             AND r.sidecar_spec -> 'high' ->> 'action' NOT IN ('require_review', 'require_approval')
        THEN jsonb_build_object('high', r.sidecar_spec -> 'high' -> 'action') ELSE '{}'::jsonb END
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'medium') = 'object' AND (r.sidecar_spec -> 'medium') - 'action' = '{}'::jsonb
             AND r.sidecar_spec -> 'medium' ->> 'action' NOT IN ('require_review', 'require_approval')
        THEN jsonb_build_object('medium', r.sidecar_spec -> 'medium' -> 'action') ELSE '{}'::jsonb END
    || CASE WHEN jsonb_typeof(r.sidecar_spec -> 'low') = 'object' AND (r.sidecar_spec -> 'low') - 'action' = '{}'::jsonb
             AND r.sidecar_spec -> 'low' ->> 'action' NOT IN ('require_review', 'require_approval')
        THEN jsonb_build_object('low', r.sidecar_spec -> 'low' -> 'action') ELSE '{}'::jsonb END
WHERE jsonb_typeof(r.sidecar_spec) = 'object';

-- The older binary would release an approved agent decision on a claim. The
-- sidecar forwarded it when it filed it, so it is spent.
UPDATE reviews SET status = 'EXECUTED'
WHERE approver_type = 'ai_agent' AND status = 'APPROVED';

DROP INDEX IF EXISTS index_reviews_sidecar_level_statement;

ALTER TABLE reviews
    DROP COLUMN IF EXISTS risk_level,
    DROP COLUMN IF EXISTS decision_reason,
    DROP COLUMN IF EXISTS approver_type;

COMMIT;
