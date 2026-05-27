# Configure Role — Edge Cases & Implementation Notes

> Temporary working document. Compiled during the React migration of the
> Configure Role page (`webapp_v2/src/pages/Roles/Configure/`). Delete
> after the items are filed in the product/engineering channel.

## Backend strip policy (gateway/api/connections/secrets.go)

The gateway returns connection secrets in two modes, picked by
`shouldRoundTripSecrets`:

| Connection shape | Behaviour |
|------------------|-----------|
| `application/ssh` | **Round-trip** — bespoke `SshRenderer` needs host/user/PASS or AUTHORIZED_SERVER_KEYS visible. |
| `httpproxy/*` (claude-code, web-application, grafana, kibana) | **Round-trip** — bespoke `ClaudeCodeRenderer` and `HttpProxyRenderer` show REMOTE_URL, HTTP headers, allow-insecure-SSL. |
| `custom/(empty subtype)`, `custom/linux-vm`, `custom/kubernetes-token` | **Round-trip** — free-form / Kubernetes renderers display env vars, configuration files, cluster URL, bearer token. |
| Everything else (catalog applications `git`/`github`/`tcp`, every `database/*`, every catalog `custom/*` — `dynamodb`, `aws-*`, `kubernetes`, `redis`, …) | **Write-only** — keys preserved, inline values blanked. References (`_aws:`, `_vaultkv1:`, `_vaultkv2:`, `_aws_iam_rds:`) and boolean strings (`"true"`/`"false"`) round-trip unchanged. |

The catalog (subtypes with metadata-driven schemas) drives the policy —
see https://github.com/hoophq/documentation/blob/main/store/connections.json.
Tests at `gateway/api/connections/secrets_test.go::TestShouldRoundTripSecrets`.

**Security trade-off** — round-tripping the five bespoke shapes means
admins on the Configure page see existing Anthropic API keys, SSH
private keys, Kubernetes bearer tokens, and so on. This matches CLJS,
which never stripped these. If a customer flags API-key exposure as a
concern, narrow `shouldRoundTripSecrets` and refactor the affected
React renderer(s) to use the Set/Replace pattern.

## React renderer dispatch

`webapp_v2/src/pages/Roles/Configure/components/renderers/index.jsx`
holds the ordered match table. First match wins; new bespoke shapes
get a new file in the same folder + a new entry here.

1. `application/ssh` → `SshRenderer` (auth-method radio)
2. `httpproxy/claude-code` → `ClaudeCodeRenderer` (Anthropic URL + Key + HTTP headers + insecure SSL + agent)
3. `httpproxy/*` → `HttpProxyRenderer` (REMOTE_URL + HTTP headers + insecure SSL + agent)
4. `custom/kubernetes-token` → `KubernetesTokenRenderer` (cluster URL + bearer token + insecure SSL + agent)
5. `custom/linux-vm` → `FreeFormCustomRenderer` (env vars + config files + command + agent)
6. Catalog subtype with schema (and not in the legacy custom free-form exclusion) → `CatalogRenderer` (Set/Replace per field + agent)
7. `type=custom` catch-all → `FreeFormCustomRenderer`
8. Anything else → `UnsupportedFallback` warning

Each renderer file owns the section titles, field schemas, and any
shape-specific logic (Bearer prefix on kubernetes-token, auth-method
radio on ssh). Shared sections (env vars list, HTTP headers list,
configuration files, command args, agent picker, insecure-SSL toggle,
predefined-field list) live in `renderers/shared/`.

## Known gaps

### G1. Legacy `cloudwatch` custom subtype (no metadata catalog match)

Connections created via the now-removed "Resource Subtype Override" Beta
have `type=custom, subtype="cloudwatch"`. The documentation catalog
only ships `aws-cloudwatch` today, so the legacy name doesn't match
the catalog renderer.

After the catalog-driven write-only flip:
- Backend `shouldRoundTripSecrets` returns **false** for `custom/cloudwatch`
  (not on the round-trip list). Values do NOT come back; they're write-only.
- React dispatch: no catalog schema for `cloudwatch`, so the dispatch
  falls through to `custom-freeform` → `FreeFormCustomRenderer`. The user
  sees empty fields (because the backend stripped them) presented as
  free-form key/value rows.
- Net effect: the user can re-enter values via free-form but cannot
  inspect existing ones, and the form loses the catalog renderer's
  structured field layout.

Fix candidates:
- Backend learns the catalog and aliases `cloudwatch → aws-cloudwatch`
  so it round-trips like a known catalog subtype OR so it renders via
  the catalog renderer. (The current rule keys on the *subtype string*,
  not on catalog presence — an alias map would fix both.)
- Migrate legacy rows to the new subtype (one-time DB script).

### G2. Icons for subtypes outside the metadata catalog

`getConnectionIcon` (in `webapp_v2/src/utils/connectionIcons.js`) looks
up `metadata[subtype]['icon-name']`. Subtypes that don't appear in the
JSON fall back to `/icons/connections/custom-ssh.svg`. Known misses:

- `kubernetes-token` (catalog has `kubernetes` but not the `-token`
  variant) — should display the Kubernetes icon, currently displays the
  SSH fallback.
- `linux-vm` (not in catalog).
- Any other subtype not in the documentation JSON.

Fix candidates:
- Add the missing variants to `hoophq/documentation:store/connections.json`.
- Keep a tiny alias map *in the JSON* (e.g. `aliases: ["kubernetes-token"]`
  on the `kubernetes` entry) so React can resolve.

### G3. AWS IAM Role gate

The "AWS IAM Role" connection-method card is only shown when subtype is
`postgres` or `mysql` (`connection_method.cljs:92`, mirrored in
`webapp_v2/src/utils/connectionPolicy.js:supportsAwsIam`). That's the
correct policy today — only RDS auth uses it.

If we ever add IAM-role auth for other AWS connection types (DynamoDB,
CloudWatch, etc.) this set needs to grow.

### G4. Inline schemas for shapes not in the JSON catalog

The five bespoke renderers carry their own field schemas because the
JSON catalog either omits them or doesn't capture the full shape:

- `claude-code` has no `credentials` array in the JSON.
- `linux-vm` isn't in the JSON.
- `kubernetes-token` isn't in the JSON.
- `ssh` has metadata credentials but the auth-method radio decides
  which field is required (PASS vs AUTHORIZED_SERVER_KEYS) — not
  expressible in the current JSON schema.
- `httpproxy/*` catalog entries only ship REMOTE_URL; the HTTP headers
  list and allow-insecure-SSL toggle live in `HttpProxyRenderer.jsx`.

When the documentation catalog grows to cover these, drop the inline
constants and route through the metadata.

## Field labels source

The mapper at `webapp_v2/src/utils/connectionsMetadataMapper.js`
preserves the JSON `name` casing for the label:
`"AWS_ACCESS_KEY_ID"` → `"AWS ACCESS KEY ID"` (CLJS-style at
`metadata_driven.cljs:50`). This is intentionally uppercase to match
the env-var feel.

If product wants title-case labels, do it in the JSON (so it's a
single source of truth), or extend the mapper with a smart-case
helper that respects acronyms.

## Where to refile

Items G1–G4 belong in the product / engineering backlog. G1 is the
biggest user-visible gap and probably wants a follow-up PR. G2/G3 are
small quality-of-life. G4 will resolve itself as the catalog grows.
