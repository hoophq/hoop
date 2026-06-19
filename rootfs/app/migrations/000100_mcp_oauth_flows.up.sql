BEGIN;
SET search_path TO private;

-- Short-lived state rows backing the MCP connection OAuth login flow.
--
-- When an admin creates an "mcp" httpproxy connection (e.g. https://mcp.figma.com/mcp)
-- the gateway acts as an OAuth 2.1 client on the admin's behalf: it discovers the
-- upstream authorization server (RFC 9728 / RFC 8414), optionally registers a client
-- via Dynamic Client Registration (RFC 7591), and drives an Authorization Code + PKCE
-- flow. This table carries the per-flow state between the three request hops:
--
--   1. authorize  -> creates the row (status='pending') and returns the auth URL.
--   2. callback   -> exchanges the code, stores the obtained token (status='completed').
--   3. token      -> returns the token to the create page once, then deletes the row.
--
-- The row is single-use and TTL-bounded (the callback rejects rows older than a fixed
-- age) to limit replay of a leaked state value. Mirrors private.federation_oauth_states.
--
-- Secrets at rest (PKCE verifier, client secret, obtained tokens) are AES-256-GCM
-- ciphertext, using the same credential vault as connection_credentials.
CREATE TABLE IF NOT EXISTS mcp_oauth_flows (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id                   UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
  user_id                  TEXT NOT NULL,
  server_url               TEXT NOT NULL,
  resource                 TEXT NOT NULL DEFAULT '',
  issuer                   TEXT NOT NULL DEFAULT '',
  authorization_endpoint   TEXT NOT NULL DEFAULT '',
  token_endpoint           TEXT NOT NULL DEFAULT '',
  client_id                TEXT NOT NULL DEFAULT '',
  client_secret_encrypted  BYTEA,
  token_auth_method        TEXT NOT NULL DEFAULT 'none',
  code_verifier_encrypted  BYTEA NOT NULL,
  scopes                   TEXT NOT NULL DEFAULT '',
  redirect_url             TEXT NOT NULL DEFAULT '',
  status                   TEXT NOT NULL DEFAULT 'pending',
  error_reason             TEXT NOT NULL DEFAULT '',
  access_token_encrypted   BYTEA,
  refresh_token_encrypted  BYTEA,
  token_type               TEXT NOT NULL DEFAULT '',
  token_expires_at         TIMESTAMP WITH TIME ZONE,
  created_at               TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_flows_org_id
  ON mcp_oauth_flows (org_id);

COMMIT;
