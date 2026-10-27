BEGIN;

SET search_path TO private;

-- Who may mint a credential for one sidecar lane: a user (subject) or a
-- group. One row names one of the two.
CREATE TABLE sidecar_lane_grants(
    id UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES orgs (id),
    sidecar_id UUID NOT NULL REFERENCES sidecars (id) ON DELETE CASCADE,
    listener_name TEXT NOT NULL,
    subject TEXT NULL,
    group_name TEXT NULL,
    created_by TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),

    CHECK (subject IS NOT NULL OR group_name IS NOT NULL)
);

CREATE INDEX sidecar_lane_grants_lane_idx ON sidecar_lane_grants (org_id, sidecar_id, listener_name);

COMMIT;
