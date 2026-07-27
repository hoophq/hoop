CREATE TABLE IF NOT EXISTS private.org_feature_flags (
    org_id      UUID NOT NULL REFERENCES private.orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT,
    PRIMARY KEY (org_id, name)
);
