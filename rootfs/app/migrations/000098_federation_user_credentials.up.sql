BEGIN;
SET search_path TO private;

-- Per-user OAuth credentials for the gcp_oauth federation provider. Unlike
-- gcp_iam (which stores a single admin service-account key per connection),
-- gcp_oauth federation mints tokens from a refresh token that each user
-- obtains by consenting once through Google's OAuth flow. One row per
-- (connection, user): the unique constraint collapses repeated consents into
-- a single stored credential.
--
-- refresh_token_encrypted is AES-256-GCM ciphertext (same vault used for the
-- admin SA key in connection_federation_configs). google_email is the
-- consented Google identity and is recorded in plaintext because it is not a
-- secret and is surfaced as the resolved principal in session audit metadata.
CREATE TABLE IF NOT EXISTS federation_user_credentials (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                   UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  connection_id            UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  user_id                  TEXT NOT NULL,
  user_email               TEXT NOT NULL,
  google_email             TEXT NOT NULL,
  refresh_token_encrypted  BYTEA NOT NULL,
  scopes                   TEXT NOT NULL DEFAULT '',
  created_at               TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at               TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_federation_user_credentials_conn_user UNIQUE (connection_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_federation_user_credentials_org_id
  ON federation_user_credentials (org_id);

-- Short-lived state rows for the gcp_oauth consent flow. The authorize
-- endpoint creates one row keyed by a random UUID (the OAuth "state"
-- parameter) and the callback endpoint consumes it to recover which
-- (org, connection, user) initiated the flow. Rows are deleted on use; stale
-- rows are rejected by age in the callback handler (see
-- connection_federation_oauth.go). This mirrors the private.login state
-- pattern used by the OIDC login flow.
CREATE TABLE IF NOT EXISTS federation_oauth_states (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  connection_id UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  user_id       TEXT NOT NULL,
  user_email    TEXT NOT NULL,
  redirect_url  TEXT NOT NULL DEFAULT '',
  created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

COMMIT;
