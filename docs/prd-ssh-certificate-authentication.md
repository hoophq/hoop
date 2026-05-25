# PRD: Transparent SSH Certificate Authentication

**Author:** andrios@hoop.dev
**Date:** 2026-05-25
**Status:** In Progress

---

## Problem

Today, Hoop SSH connections require administrators to configure either a password or an authorized public key for each connection. This approach has several limitations:

1. **Static credentials are a security risk.** Passwords and long-lived SSH keys can be leaked, shared, or forgotten in configuration. They don't expire naturally and require manual rotation.

2. **No per-session identity.** When multiple users connect through the same SSH connection, the remote server sees the same credential. There is no way to attribute individual sessions to specific users at the SSH protocol level.

3. **Manual key management burden.** Administrators must generate, distribute, and rotate SSH keys for every connection. As the number of connections grows, this becomes operationally expensive.

4. **Certificates are an industry best practice** adopted by companies like Meta, Netflix, and Uber for SSH access, but implementing them typically requires deploying and maintaining a separate Certificate Authority infrastructure (e.g., Vault SSH, step-ca).

## Solution

Hoop will automatically issue short-lived SSH user certificates for every SSH session. The feature is fully transparent: administrators don't configure anything related to certificates when creating connections, and users receive certificates automatically when they connect.

### How It Works

1. **Gateway generates a Certificate Authority (CA) key pair** on first SSH server startup. This is a one-time automatic operation stored alongside the existing SSH server configuration.

2. **When a user opens an SSH session**, the gateway generates a unique, ephemeral key pair and signs it with the CA to produce a short-lived SSH user certificate. The certificate:
   - Is scoped to the SSH username configured on the connection
   - Has a validity period matching the user's authentication token duration
   - Is unique per session (never shared between users or sessions)
   - Exists only in memory during the session (never written to database or disk)

3. **The agent uses the certificate** as the first authentication method when connecting to the remote SSH server. If the remote server trusts the CA, authentication succeeds. If not, it falls back silently to password or key authentication.

4. **The remote SSH server** is configured once by the administrator to trust Hoop's CA public key via standard OpenSSH `TrustedUserCAKeys` configuration.

### User Journeys

**Administrator setting up certificate-based SSH access:**

1. Enable the SSH server in Hoop gateway settings (existing flow, no changes)
2. Retrieve the CA public key from the gateway API
3. Add the CA public key to the target SSH server's `TrustedUserCAKeys` configuration
4. Create SSH connections as usual (HOST, USER, PORT) - no password or key needed
5. Done. All sessions to that server now use certificates.

**User connecting via SSH:**

1. Run `hoop connect my-ssh-server` (no changes to existing flow)
2. The certificate is issued and used automatically - the user sees no difference
3. Session is authenticated, recorded, and governed by Hoop policies as before

**Gradual adoption / mixed environments:**

1. Administrator creates an SSH connection with both a password and the CA trust configured on the remote server
2. Certificate auth is attempted first; if the remote server trusts the CA, it succeeds
3. If certificate auth fails (e.g., CA not yet configured on that server), password auth is used as fallback
4. No downtime or breaking changes during migration

## Requirements

### Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1 | The gateway must auto-generate an Ed25519 CA key pair when the SSH server starts for the first time | Must |
| FR-2 | Each SSH session must receive a unique ephemeral certificate signed by the CA | Must |
| FR-3 | Certificate validity must match the user's JWT token expiration | Must |
| FR-4 | Certificates must include the SSH connection's configured username as the principal | Must |
| FR-5 | Certificate and ephemeral private key must never be persisted to the database | Must |
| FR-6 | Certificate auth must be attempted before password/key auth | Must |
| FR-7 | If certificate auth fails, the system must fall back to password or key auth silently | Must |
| FR-8 | An API endpoint must expose the CA public key for administrator retrieval | Must |
| FR-9 | The CA private key must not be exposed through any API | Must |
| FR-10 | The CA key must be preserved across SSH server configuration updates | Must |
| FR-11 | The feature must work without any changes to existing SSH connection creation flow | Must |
| FR-12 | Certificates must include standard SSH extensions: pty, agent-forwarding, port-forwarding | Should |

### Non-Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| NFR-1 | No external services or containers required (self-contained in gateway) | Must |
| NFR-2 | Certificate issuance must add negligible latency to session establishment (< 50ms) | Must |
| NFR-3 | The CA key must use Ed25519 (fast, small, modern) | Must |
| NFR-4 | Existing SSH connections must continue to work without modification | Must |
| NFR-5 | The feature must not require database migrations | Should |

## Scope

### In Scope

- CA key pair auto-generation and storage
- Per-session certificate issuance
- Certificate injection into the session transport layer
- Agent-side certificate authentication with the remote SSH server
- API endpoint for CA public key retrieval
- Fallback to existing auth methods when certificate auth is unavailable

### Out of Scope

- Certificate revocation lists (CRL) - certificates are short-lived enough that revocation is unnecessary
- Host certificate verification (verifying the remote server's identity via certificates)
- UI for CA key management or certificate visibility
- Multi-CA support (e.g., different CAs per connection or agent)
- Certificate auth for the Hoop SSH proxy server itself (the gateway-facing SSH endpoint) - this uses its own credential-based authentication

## Security Considerations

- **Short-lived certificates** eliminate the risk of credential leakage. Even if intercepted, a certificate expires with the user's session token.
- **Per-session ephemeral keys** ensure that compromising one session's key material doesn't affect any other session.
- **CA private key protection**: stored in the database alongside other server configuration secrets (same security boundary as the SSH host key), never exposed via API.
- **No new attack surface**: the CA public key is not sensitive (it's meant to be distributed to SSH servers). The certificate issuance happens entirely within the gateway process.
- **Fallback behavior**: if an attacker removes certificate auth, the system falls back to password/key auth - it doesn't fail open to unauthenticated access.

## Success Metrics

| Metric | Target |
|--------|--------|
| SSH connections created without password or key (certificate-only) | Trackable via connections with no PASS/AUTHORIZED_SERVER_KEYS configured |
| Session establishment latency increase | < 50ms compared to baseline |
| Zero breaking changes to existing SSH connections | 100% backward compatibility |

## Rollout Plan

1. **Phase 1 (this PR):** Gateway-side CA generation, certificate issuance, agent transport - all transparent, no user-facing changes
2. **Phase 2:** Agent-side certificate authentication (libhoop private repo) - completes the end-to-end flow
3. **Phase 3:** Documentation for administrators on configuring `TrustedUserCAKeys` on remote servers
4. **Future:** UI for viewing the CA public key, connection-level indicators showing certificate vs. password auth
