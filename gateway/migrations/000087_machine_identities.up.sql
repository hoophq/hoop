BEGIN;
SET search_path TO private;

CREATE TABLE machine_identities (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id           UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  connection_names TEXT[] NOT NULL DEFAULT '{}',
  created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_machine_identities_org_name UNIQUE (org_id, name)
);

ALTER TABLE connection_credentials ADD COLUMN secret_key TEXT NULL;

CREATE TABLE machine_identity_credentials (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                   UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  machine_identity_id      UUID NOT NULL REFERENCES machine_identities(id) ON DELETE CASCADE,
  connection_credential_id UUID NOT NULL REFERENCES connection_credentials(id) ON DELETE CASCADE,
  connection_name          TEXT NOT NULL,
  created_at               TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_mi_creds_identity_conn UNIQUE (machine_identity_id, connection_name)
);

CREATE TABLE machine_identities_attributes (
  org_id                UUID NOT NULL,
  machine_identity_name TEXT NOT NULL,
  attribute_name        VARCHAR(255) NOT NULL,
  PRIMARY KEY (org_id, machine_identity_name, attribute_name),
  FOREIGN KEY (org_id, machine_identity_name) REFERENCES machine_identities(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE,
  FOREIGN KEY (org_id, attribute_name) REFERENCES attributes(org_id, name) ON UPDATE CASCADE ON DELETE CASCADE
);

ALTER TABLE sessions ADD COLUMN machine_identity_id UUID;
ALTER TABLE sessions ADD COLUMN identity_type TEXT NOT NULL DEFAULT 'user';

COMMIT;
