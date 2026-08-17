BEGIN;
SET search_path TO private;

-- Answer to the onboarding "How did you hear about Hoop?" survey, used to
-- measure which acquisition channel (free tools, AI assistants, communities)
-- originated each sign up.
--
-- One answer per user: the column stays NULL until the user submits and the
-- API refuses to overwrite a non-NULL value, so the first answer wins.
-- NULL therefore means "never answered", which is also what drives whether the
-- survey is still shown (together with the 7 day window measured from
-- created_at).
ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_origin VARCHAR(64) NULL;

-- Free text detail captured for the 'other' option. NULL for every other
-- answer, so the two columns can never disagree about which option was picked.
ALTER TABLE users ADD COLUMN IF NOT EXISTS signup_origin_other VARCHAR(255) NULL;

COMMIT;
