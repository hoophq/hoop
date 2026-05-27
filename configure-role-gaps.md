# Configure Role — Edge Cases & Implementation Notes

> Temporary working document. Compiled during the React migration of the
> Configure Role page (`webapp_v2/src/pages/Roles/Configure/`). Delete
> after the items are filed in the product/engineering channel.

## Backend strip policy (gateway/api/connections/secrets.go)

The gateway returns connection secrets in two modes:

| Connection shape | Behaviour |
|------------------|-----------|
| `type=custom` AND (`subtype=""` OR subtype ∈ `{tcp, httpproxy, ssh, linux-vm, claude-code}`) | **Round-trip plaintext** — values come back base64-encoded so the free-form editor can show / edit them. |
| Everything else (database, application, httpproxy, custom + non-free-form subtype) | **Write-only** — keys are preserved, inline values blanked. Reference values (`_aws:`, `_vaultkv1:`, `_vaultkv2:`, `_aws_iam_rds:`) and boolean strings (`"true"` / `"false"`) round-trip unchanged. |

Predicate: `isFreeFormCustom(conn *models.Connection)` at
`gateway/api/connections/secrets.go:86`. Tests at
`gateway/api/connections/secrets_test.go:108-141`.

## React dispatch (webapp_v2/src/pages/Roles/Configure/components/CredentialsTab.jsx)

Renderer is picked by the first matching entry in `buildRenderers(getSchema)`:

1. `database` + has metadata schema → catalog renderer
2. `application` + `{ssh, git, github}` → SSH renderer
3. `httpproxy` + `claude-code` → Claude Code renderer (inline schema)
4. `httpproxy` (any other) → HTTP proxy generic (inline schema)
5. `custom` + `kubernetes-token` → Kubernetes token (inline schema)
6. `custom` + subtype NOT in `{tcp, httpproxy, ssh, linux-vm, claude-code}` + has metadata schema → catalog renderer
7. `custom` (everything else) → free-form `CustomCredentials`

Mirrors CLJS `credentials_tab.cljs:11-33` with one intentional divergence:
when CLJS would route to `metadata-driven` and the JSON has no schema, it
renders `nil` (blank tab). React falls through to free-form so the user
can at least see / edit existing envvars on legacy rows.

## Known gaps

### G1. Legacy `cloudwatch` / `dynamodb` custom subtypes

Connections created via the now-removed "Resource Subtype Override" Beta
have `type=custom, subtype="cloudwatch"` or `"dynamodb"`. The
documentation catalog only ships `aws-cloudwatch` and `dynamodb`
entries today — the latter happens to match, the former doesn't.

For legacy `cloudwatch`:
- Backend `isFreeFormCustom` returns false → values stripped.
- React `custom-catalog` doesn't match (no schema) → falls through to
  free-form `CustomCredentials`.
- Net effect: user sees the envvar keys (`AWS_ACCESS_KEY_ID`,
  `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`) with **empty** value fields.
  Can re-enter values but can't see existing ones.

Fix candidates (none implemented in this delivery):
- Backend learns the catalog and uses metadata-driven strip decision
  (write-only when subtype is a catalog credential type, free-form
  otherwise). Right place to put the rule, biggest surface change.
- Backend adds an alias map `cloudwatch → aws-cloudwatch`. Quick, but
  alias maps tend to grow over time.
- Migrate legacy rows to the new subtype (one-time DB script).

Current workaround: re-create the connection with the new
`aws-cloudwatch` subtype, or edit via the legacy CLJS editor while it
still exists.

### G2. Icons for subtypes outside the metadata catalog

`getConnectionIcon` (in `webapp_v2/src/utils/connectionIcons.js`) looks
up `metadata[subtype]['icon-name']`. Subtypes that don't appear in the
JSON fall back to `/icons/connections/custom-ssh.svg`. Known misses:

- `kubernetes-token` (catalog has `kubernetes` but not the `-token`
  variant) — should display the Kubernetes icon, currently displays the
  SSH fallback.
- `httpproxy` (catalog has `web-application` as the generic, not
  `httpproxy`) — same fallback.
- Any subtype not in the documentation JSON.

Fix candidates:
- Add the missing variants to `hoophq/documentation:store/connections.json`.
- Keep a tiny alias map *in the JSON* (e.g. `aliases: ["kubernetes-token"]`
  on the `kubernetes` entry) so React can resolve.

### G3. Per-field source selector missing on free-form CustomCredentials

When the user picks "Secrets Manager" from the Connection Method picker
on a free-form custom connection, each row should offer a per-field
provider selector (Vault KV / AWS Secrets Manager / Manual) — that's
what CLJS does at `webapp/.../setup/configuration_inputs.cljs:153,170`.

React `CustomCredentials.jsx` doesn't consume `availableSources` (the
prop is passed in from `CredentialsTab` but the component ignores it),
so the row always uses a plain `PasswordInput`. The Connection Method
pick is still visible at the top, but the per-row source choice is
not exposed.

Fix: thread `availableSources` into `EnvvarRow`, render the source
selector adornment when non-null. Mirrors how
`PredefinedFieldsCredentials.jsx` already does it for catalog connections.

### G4. AWS IAM Role gate

The "AWS IAM Role" connection-method card is only shown when subtype is
`postgres` or `mysql` (`connection_method.cljs:92`, mirrored in
`webapp_v2/src/utils/connectionPolicy.js:supportsAwsIam`). That's the
correct policy today — only RDS auth uses it.

If we ever add IAM-role auth for other AWS connection types (DynamoDB,
CloudWatch, etc.) this set needs to grow.

### G5. Inline schemas for connection types not in the JSON catalog

`webapp_v2/src/pages/Roles/Configure/components/CredentialsTab.jsx`
holds inline `HTTPPROXY_FIELDS`, `CLAUDE_CODE_FIELDS`,
`KUBERNETES_TOKEN_FIELDS` constants and `SSHCredentials.jsx` holds
`SSH_FIELDS`, because:

- `claude-code` has no `credentials` array in the JSON.
- The generic `httpproxy` subtype isn't in the JSON (catalog uses
  `web-application`).
- `kubernetes-token` isn't in the JSON.
- `ssh` has metadata credentials but the auth-method radio decides which
  field is required (PASS vs AUTHORIZED_SERVER_KEYS) — not expressible
  in the current JSON schema.

When the documentation catalog grows to cover these, drop the inline
constants and route through the metadata.

### G6. Connection Method picker visibility

CLJS shows the picker on every credentials tab (`server.cljs:43`,
`server.cljs:137`, `server.cljs:186`, `network.cljs:34`,
`network.cljs:84`, `metadata_driven.cljs:121-139`,
`claude_code_edit.cljs:59`). React matches this behaviour after the
recent fix — the picker now renders for database, application,
httpproxy, custom (any subtype), and kubernetes-token.

If we decide some renderer shouldn't expose the picker (e.g. if a
connection type is forced to one method), gate at
`CredentialsTab.jsx`'s `ConnectionMethodSection` call site.

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

Items G1–G6 belong in the product / engineering backlog. G1 is the
biggest user-visible gap and probably wants a follow-up PR. G2/G3 are
small quality-of-life. G5 will resolve itself as the catalog grows.
