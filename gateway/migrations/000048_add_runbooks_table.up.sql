BEGIN;

SET search_path TO private;

CREATE TABLE runbooks (
  id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
  org_id UUID NOT NULL REFERENCES orgs (id),
  repository_configs JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  UNIQUE (org_id)
);

CREATE TABLE runbook_rules (
  id UUID DEFAULT uuid_generate_v4() PRIMARY KEY,
  org_id UUID NOT NULL REFERENCES orgs (id),
  name TEXT NOT NULL,
  description TEXT,
  user_groups TEXT[] NOT NULL,
  connections TEXT[] NOT NULL,
  runbooks JSONB NOT NULL DEFAULT '[]'::JSONB,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

COMMIT;
