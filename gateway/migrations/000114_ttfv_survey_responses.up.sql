BEGIN;
SET search_path TO private;

-- Answers to the in-app TTFV survey ("Did you get done what you came here to
-- do?"). TTFV is the duration between orgs.created_at and the first answer
-- with reached_value = true, which is why every answer is a row instead of a
-- column on orgs: a "no" is a valid data point that has to be recorded and
-- then followed by another ask, and keeping the full history means the metric
-- can be recomputed if the policy behind it changes.
--
-- user_id is TEXT and unconstrained on purpose: it holds users.subject, which
-- is the identity the API context carries, and the answer must survive the
-- user being deleted. The org is what the metric is about, so only that FK
-- cascades.
CREATE TABLE IF NOT EXISTS ttfv_survey_responses (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL,
    reached_value  BOOLEAN NOT NULL,
    activity       TEXT,
    activity_other TEXT,
    created_at     TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Every read is "the latest answers for this org": the terminal-yes check and
-- the cooldown window both resolve against the head of this index.
CREATE INDEX IF NOT EXISTS ttfv_survey_responses_org_id_idx
    ON ttfv_survey_responses (org_id, created_at DESC);

-- At most one confirmed value per organization, as a database invariant rather
-- than as something the writing statement checks for itself. TTFV is a single
-- moment per organization, so a second confirmation is a contradiction and not
-- merely a duplicate — and under READ COMMITTED two concurrent submissions both
-- see an organization that has not confirmed yet, both pass that check and both
-- insert. This index is what actually decides between them; the loser gets a
-- unique violation, which the caller reports as a conflict.
CREATE UNIQUE INDEX IF NOT EXISTS ttfv_survey_responses_one_confirmed_value_idx
    ON ttfv_survey_responses (org_id) WHERE reached_value;

COMMIT;
