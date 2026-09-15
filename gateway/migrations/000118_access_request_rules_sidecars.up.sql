BEGIN;

SET search_path TO private;

-- The sidecars a sidecar rule authorizes to file reviews, by name like
-- connection_names. A sidecar created again under a deleted one's name is
-- authorized again.
ALTER TABLE access_request_rules ADD COLUMN IF NOT EXISTS sidecar_names TEXT[] NOT NULL DEFAULT '{}';

-- A rule targets connections or sidecars, never both. Every connection lookup
-- matches connection_names, so a sidecar rule stays out of them, and no writer
-- can retype a rule that still lists sidecars.
ALTER TABLE access_request_rules ADD CONSTRAINT access_request_rules_sidecar_targets
    CHECK (
        (access_type = 'sidecar' AND connection_names = '{}')
        OR (access_type <> 'sidecar' AND sidecar_names = '{}')
    );

COMMIT;
