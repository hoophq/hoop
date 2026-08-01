BEGIN;
SET search_path TO private;

-- Durable OAuth grants for MCP Gateway (mcpproxy) connections.
--
-- private.mcp_oauth_flows is transient: it carries state across the three
-- login hops and is deleted once the token leaves the gateway. That makes the
-- obtained credential a one-shot — the refresh token is discarded and the
-- connection breaks the moment the provider's TTL elapses.
--
-- This table is the other half: the grant that outlives the login. A completed
-- flow is adopted into a row here keyed by the connection it was authorized
-- for, and the session-open path renews the access token from the stored
-- refresh token (see gateway/services/mcp_oauth_grant.go).
--
-- The endpoint/client columns are copied from the flow rather than
-- rediscovered at refresh time: the authorization server that issued the grant
-- is the only one that can renew it, and re-running RFC 8414 discovery on the
-- session-open path would add a network hop that can only disagree.
--
-- user_id is '' for a connection-wide grant, which is what the current UI
-- produces: one admin authorizes and every user of the connection shares the
-- credential. The column exists so per-user grants (ADR-0004) become a second
-- row rather than a migration.
--
-- Secrets at rest (client secret, tokens) are AES-256-GCM ciphertext from the
-- same credential vault as connection_credentials.
CREATE TABLE IF NOT EXISTS mcp_oauth_grants (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                   UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  connection_id            UUID NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  user_id                  TEXT NOT NULL DEFAULT '',
  server_url               TEXT NOT NULL,
  resource                 TEXT NOT NULL DEFAULT '',
  issuer                   TEXT NOT NULL DEFAULT '',
  token_endpoint           TEXT NOT NULL DEFAULT '',
  client_id                TEXT NOT NULL DEFAULT '',
  client_secret_encrypted  BYTEA,
  token_auth_method        TEXT NOT NULL DEFAULT 'none',
  scopes                   TEXT NOT NULL DEFAULT '',
  access_token_encrypted   BYTEA,
  refresh_token_encrypted  BYTEA,
  token_type               TEXT NOT NULL DEFAULT '',
  token_expires_at         TIMESTAMP WITH TIME ZONE,
  created_at               TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at               TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT uq_mcp_oauth_grants_connection_user UNIQUE (connection_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_grants_org_id
  ON mcp_oauth_grants (org_id);

COMMIT;
