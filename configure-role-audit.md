# Configure Role — Credentials Tab Audit

> Auditoria CLJS vs React feita após os commits `0c56ee4` → `90a7f37`.
> Documento de trabalho — deletar quando os itens forem filed.

**Branch state:** post-refactor. Backend write-only by default; round-trip only for the 5 frontend-mounted shapes.

---

## 1. Paridade por renderer

### CatalogRenderer (databases, application/git, application/github, application/tcp, custom catalog subtypes)

**CLJS** (`metadata_driven.cljs:121-139`):
1. `connection-method/main` (selector — *outside* this code path in React, lives in CredentialsTab)
2. `metadata-credentials` → Heading "Environment credentials" + fields grid
3. `agent-selector/main`

**React** (`CatalogRenderer.jsx:17-30`):
1. Title "Environment credentials"
2. `PredefinedFields`
3. `AgentSelectorSection`

**Differences:**
- Identical ordering. Helper-text/description (`field.description`) is rendered by CLJS (`metadata_driven.cljs:32`) as `helper-text` on each input. **React `PredefinedFields.jsx:100-156` does not pass `description` through** — silent regression on AWS-* / kubernetes / redis subtypes whose catalog entries carry descriptions.

---

### SshRenderer (application/ssh, plus git/github in CLJS)

**CLJS** (`server.cljs:114-155`):
1. Heading "SSH Configuration"
2. `connection-method/main "ssh"` — *outside* this path in React
3. Radio group: Username & Password / Private Key
4. Filtered fields (host/port/user + pass | authorized_server_keys)
5. `agent-selector/main`

**React** (`SshRenderer.jsx:71-103`):
1. Title "SSH Configuration" + dimmed subtitle "Provide SSH information to set up your connection." (CLJS has "to setup" — see `server.cljs:135`)
2. Radio Group
3. `PredefinedFields` (filtered)
4. `AgentSelectorSection`

**Differences:**
- Copy diff: "set up" (React) vs "setup" (CLJS). Minor — React's is the correct verb.
- **Routing diff (intentional but worth flagging):** CLJS uses `ssh-credentials` for `application/ssh` *and* `application/git` *and* `application/github` (`credentials_tab.cljs:27-32`). React only routes `application/ssh` → `SshRenderer`; git/github fall through to `CatalogRenderer`. The backend's `shouldRoundTripSecrets` agrees with React (`secrets.go:107`: `return sub == "ssh"`). Confirmed intentional. Git/GitHub get write-only HOST/USER catalog fields now.
- Auth-method switching: CLJS stores `auth-method` per-session in re-frame state (`server.cljs:104-112`). React derives initial method from connection state (`SshRenderer.jsx:36-40`) and stages a delete on switch (`L57-69`). React's approach is cleaner (no separate state field on save).

---

### ClaudeCodeRenderer (httpproxy/claude-code)

**CLJS** (`claude_code_edit.cljs:54-99`):
1. `connection-method/main` — *outside* in React
2. Box "Basic info": Anthropic URL + Anthropic API Key
3. `http-headers-section`
4. Switch "Allow insecure SSL"
5. `agent-selector/main`
6. **Lifecycle quirk** (`L41-52`): on first render, if an env-var with key `X_API_KEY` exists, *moves* it into `HEADER_X_API_KEY` credentials and filters it out of env-vars. Legacy data migration.

**React** (`ClaudeCodeRenderer.jsx:34-53`):
1. Title "Basic info" + `PredefinedFields` (URL + API Key)
2. `HttpHeadersSection` (excluding `envvar:HEADER_X_API_KEY`)
3. `AllowInsecureSslSection`
4. `AgentSelectorSection`

**Differences:**
- **Missing: legacy `X_API_KEY` → `HEADER_X_API_KEY` migration.** No code path in React handles connections that have the old shape. Likely fine if all such connections have already been migrated server-side, but if any survive in the DB the user will see a phantom blank "Anthropic API Key" and a `X_API_KEY` row in their env-vars list — confusing.
- React passes `placeholder: 'https://api.anthropic.com'` but CLJS *also* defaults the value to `"https://api.anthropic.com"` when empty (`claude_code_edit.cljs:24`). React shows blank with placeholder hint. Minor UX divergence.
- CLJS reads `:insecure` from `claude-code-credentials` (`L33-38`), normalised. React reads from `connection.secret['envvar:INSECURE']` directly via `AllowInsecureSslSection`. Equivalent but a different path.

---

### HttpProxyRenderer (httpproxy/web-application, grafana, kibana)

**CLJS** (`network.cljs:11-65`):
1. `connection-method/main "httpproxy"`
2. Text "Environment credentials" + Remote URL input
3. `http-headers-section`
4. Allow insecure SSL switch
5. `agent-selector/main`

**React** (`HttpProxyRenderer.jsx:25-44`):
1. Title "Environment credentials" + `PredefinedFields` (remote_url)
2. `HttpHeadersSection`
3. `AllowInsecureSslSection`
4. `AgentSelectorSection`

**Differences:**
- Functional parity. CLJS uses `:size "4" :weight "bold"` Text; React uses `Title order={4}` — same visual weight.

---

### KubernetesTokenRenderer (custom/kubernetes-token)

**CLJS** (`server.cljs:157-226`):
1. `connection-method/main "kubernetes-token"`
2. Cluster URL input (with optional source selector)
3. Authorization token input — **CLJS strips `"Bearer "` prefix for display** (`L167-169`)
4. Allow insecure SSL Switch
5. `agent-selector/main`

**React** (`KubernetesTokenRenderer.jsx:102-148`):
1. Title "Kubernetes token" (CLJS has no equivalent heading inside this form)
2. Cluster URL → `SourcedInput`
3. Authorization token → `SourcedInput` with `stripBearer` transform
4. `AllowInsecureSslSection`
5. `AgentSelectorSection`

**Differences:**
- React renders a "Kubernetes token" `Title` (`L105`) which CLJS doesn't have. Minor cosmetic.
- **Bearer prefix handling:** CLJS strips on display (`L167-169`) and re-prefixes on save (`process_form.cljs:91-100`) only when source is `manual-input`. React does *both* on render (`KubernetesTokenRenderer.jsx:88-94`) — strips display, re-prefixes on write. **Same end-state, but React applies the prefix *while typing* — every keystroke encodes a `"Bearer "`-prefixed value into stagedSecrets**. The user sees the stripped value, the wire gets the prefix. Either approach works; React's is more eager but also means `decodeSecretValue` strips it again next render. Slightly wasteful but functionally correct.

---

### FreeFormCustomRenderer (custom/(empty), custom/linux-vm, custom/tcp/httpproxy/ssh fallbacks)

**CLJS** (`server.cljs:36-75`):
1. `connection-method/main "custom"`
2. `configuration-inputs/environment-variables-section`
3. `configuration-inputs/configuration-files-section`
4. Heading "Additional command" + multi-select text input
5. Optional `resource-subtype-override-section` (update mode only)
6. `agent-selector/main`

**React** (`FreeFormCustomRenderer.jsx:14-26`):
1. `EnvironmentVariablesSection`
2. `ConfigurationFilesSection`
3. `CommandArgsSection`
4. `AgentSelectorSection`

**Differences:**
- **Missing: `resource-subtype-override-section`.** CLJS exposes a "Resource subtype override" select on the credentials step *in update mode only* (`server.cljs:71-72`) for `custom/dynamodb` and `custom/cloudwatch` legacy connections — lets the user migrate the underlying subtype. **No React equivalent.** Unclear whether this matters for the Configure Role tab (the override is mostly a connection-setup wizard concern). Probably out of scope here.
- Copy: CLJS uses "Add variable values to use in your resource role" (`configuration_inputs.cljs:184`). React uses "Include environment variables to be used in your resource role" (`EnvironmentVariablesSection.jsx:162`). Both readable, divergent wording.

---

## 2. Save handler — `process_form.cljs` vs `buildSecretsPatch`

`buildSecretsPatch` (`store.js:459-478`) is **dumb on purpose**: it walks `stagedSecrets`, applies renames as `delete-old + replace-new`, and drops the placeholder sentinels. All shape-specific work has been pushed *into the renderers* (e.g., Bearer prefix in `KubernetesTokenRenderer.jsx:88-94`, source encoding in `secretsCodec.encodeSecretForSource`, headers prefixing in `HttpHeadersSection.jsx:199-201`).

**Transformations CLJS does that React does NOT do at save time** (because they're either rendered in or genuinely missing):

| CLJS transform | Where (process_form.cljs) | React equivalent | Status |
|---|---|---|---|
| Upper-case every env-var key, **except** `HEADER_*` for httpproxy | `helpers.cljs:42-44` (`config->json`) | `PredefinedFields.jsx:71` (`field.key.toUpperCase()`), `EnvironmentVariablesSection.jsx:220` (`'envvar:' + newName.toUpperCase()`). HEADERS preserve case (`HttpHeadersSection.jsx:199-201`) | **Done in renderers** |
| Filter blank-value env-vars (e.g. SSH where one optional field is absent) | `process_form.cljs:101-106, 146-148, 195-200` | None — React just stores empty | **Diff**: React sends `envvar:PORT=""` (which becomes a delete on the wire — mergeSecrets `secrets.go:182-188`) when CLJS would not send the key at all. Functionally equivalent because the backend interprets empty as delete, but React generates more wire payload. |
| Bearer prefix on `HEADER_AUTHORIZATION` (kubernetes-token + manual source) | `process_form.cljs:91-100` | `KubernetesTokenRenderer.jsx:88-94` | **Done in renderer** (every keystroke vs once at save) |
| `_aws_iam_rds:` prefix on USER/PASS for AWS IAM mode | `process_form.cljs:113-138` | **MISSING.** No AWS IAM code path on save in React — `availableSources` ignores AWS IAM, `encodeSecretForSource` only knows `_aws:`, `_vaultkv1:`, `_vaultkv2:` | **GAP** — AWS IAM mode isn't actually wired beyond the SelectionCard. `supportsAwsIam()` returns true for postgres/mysql; user can pick AWS IAM mode but nothing applies the prefix on save. Setting `forceNewState` only blanks the inputs. |
| Strip blank tags | `process_form.cljs:33-55` (`filter-valid-tags`) | `store.js:109-117` (`pruneEmptyTags`) | Parity |
| Strip blank `mandatory_metadata_fields` | `process_form.cljs:214-215` | `store.js:154-159` | Parity |
| filesystem:SSH_PRIVATE_KEY appends trailing `\n` | `helpers.cljs:46-48` | None | **MISSING** — could break SSH key uploads. Niche but real. |
| Reject unnamed placeholder rows | None (silently passes junk through) | `store.js:490-507` | **React is better** here — CLJS would persist `filesystem:NEW_FILE_1` if user typed content without naming. |

---

## 3. Over-engineering no React

### `PredefinedFields.jsx` — dual code path

**Current state**: branches on `isRoundTrippedPlain` (`L93-126` SourcedInput) vs SecretField fallback (`L128-156`). The backend ships round-tripped values **only for the 5 bespoke shapes** (`secrets.go:94-116`), which use renderers that wrap `PredefinedFields`. Specifically:

- `SshRenderer` → fields round-trip (host, user, etc.) — uses `SourcedInput` branch
- `CatalogRenderer` (databases, git, github, tcp, catalog custom) → write-only — uses `SecretField` branch
- `ClaudeCodeRenderer` & `HttpProxyRenderer` → REMOTE_URL/HEADER_X_API_KEY round-trip — uses `SourcedInput` branch

So both branches are live. Not dead code, but the gating logic at `L93-94` (`!forceNewState && encodedValue && !isReference && !staged`) is subtle. **Worth keeping as-is** but the comment block `L29-45` is the most valuable line in the file — don't lose it.

**Minor cleanup possibility (~5-10 lines):** the inline `stagedValue={...}` decode at `L140-143` duplicates the `PROVIDER_PREFIX_RE.replace` logic from `L96-99`. A tiny local helper `decodeForDisplay(encoded)` would remove the duplication.

### Sentinel pattern (`envvar:NEW_KEY_N`, `filesystem:NEW_FILE_N`, `envvar:HEADER_NEW_HEADER_N`)

**React** (`EnvironmentVariablesSection.jsx:146-155`, `HttpHeadersSection.jsx:144-153`, `ConfigurationFilesSection.jsx:97-107`, `secretsCodec.js:6-7`, `store.js:72-74`): A `PLACEHOLDER_KEY_RE` matches the sentinel; the empty-state effect auto-adds one; `buildSecretsPatch` drops them; `save()` rejects if the user filled content without naming.

**CLJS** (`configuration_inputs.cljs:121-179`): Uses a separate buffer in form-data — `:credentials :current-key`, `:current-value`, `:current-file-name`, `:current-file-content`. The buffer has its own dispatch events and renders a *separate* input pair, not a row in the list. "Add" button promotes the buffer into the list.

**Verdict:** CLJS's buffer pattern is arguably cleaner (no key collision concerns, no special-case regex), but React's sentinel pattern lets each row live in one place (the staged map) so the same code paths handle saved & new rows uniformly. The complexity in React is **the `PLACEHOLDER_KEY_RE` + the unnamed-on-save check** — both ~3 lines each. **Not over-engineered.** Keep.

**One legitimate trap (`EnvironmentVariablesSection.jsx:172-178`, `ConfigurationFilesSection.jsx:125-131`):** the "isPlaceholder ONLY when not isExisting" guard is correct but the comment is unnecessarily long. The actual logic is fine.

### `forceNewState` prop

Threaded through 5 components: `CredentialsTab → CredentialsBody → buildRenderers' render → {SshRenderer, ClaudeCodeRenderer, HttpProxyRenderer, KubernetesTokenRenderer, CatalogRenderer} → PredefinedFields`. **Five hops just to toggle a boolean.** Consumed only by `PredefinedFields.jsx:74` and `KubernetesTokenRenderer.jsx:59,72`.

**Option:** put it in the store. `useConfigureRoleStore` already has `clearStagedSecrets`; a sibling `setForceNewState` would let any consumer subscribe. Saves ~15 lines of prop plumbing. **Medium-priority cleanup**, not urgent — current setup works and is explicit.

### `availableSources` prop

Same pattern — passed top-down through 4-5 hops. Same recommendation: could live in the store. Slightly more justified than `forceNewState` because the per-form provider state (`secretsProvider`) does belong in component state currently — moving it to the store would require also lifting the provider toggle UI.

### Dispatch table in `renderers/index.jsx`

7 entries, `match`/`render` shape. **CLJS equivalent** (`credentials_tab.cljs:11-33`) uses a nested `case`/`cond` — ~20 lines.

The dispatch table works but it's overbuilt for 7 fixed entries. The `match` functions are simple predicates; the `render` thunks just pass props through. A single `function pickRenderer(connection, getSchema)` switch statement would be equally readable in ~30 lines. **Low priority.** The `buildRenderers(getSchema)` factory pattern (closure over `getSchema`) is the main thing it buys, but you could pass `getSchema` to `pickRenderer` directly.

### `dependsOnCatalog` in `CredentialsTab.jsx:109-124`

The heuristic enumerates the connection type/subtype cases that need the catalog. It's correct but **inverted from `buildRenderers`** — both encode the same routing rule. **Tight coupling:** if anyone adds a new bespoke shape, they have to remember to update *both* `buildRenderers` entries *and* `dependsOnCatalog`. **Suggestion:** mark the renderer entries with `requiresCatalog: false`, then `dependsOnCatalog = renderer.requiresCatalog`. One source of truth.

---

## 4. Features que o CLJS tem e o React não

### Input validation
- **POSIX env-key validation** (`configuration_inputs.cljs:10-37`): `valid-first-char?` + `valid-posix?` reject keys starting with a digit or containing non-alphanumeric chars during typing. **React lets you type anything** (`EnvironmentVariablesSection.jsx:60-71`) and just upper-cases on blur. User can save `123_invalid` which the agent will reject.
- **Header-key non-whitespace validation** (`configuration_inputs.cljs:16-17, 81-97`). React allows whitespace until `onBlur` calls `trim()` then accepts whatever's left (`HttpHeadersSection.jsx:54-61`). Less strict but probably fine — non-whitespace is loose.
- **Case conversion**: CLJS env vars get *uppercased per keystroke* (`configuration_inputs.cljs:55,58`); config file names too. React defers to `onBlur` and only when committing. **Visible diff** while typing: CLJS shows `API_KEY` as you type `api_key`; React shows `api_key` until blur. Either is fine; document the difference if a user reports it.

### Helper text
- CLJS `metadata_driven.cljs:31, 40` passes `:helper-text description` from the catalog schema (some entries carry one). **React `PredefinedFields` does not render description.** Silent regression for AWS-* / kubernetes catalog connections that have helper text.

### Trailing newline on SSH private key
- `helpers.cljs:46-48`: prepends a `\n` to `filesystem:SSH_PRIVATE_KEY` content. **React: missing.** Could break SSH key parsing on the agent for users who paste a key without trailing newline. (Mostly belongs in the connection-setup wizard, but Configure Role can also create/edit such files.)

### Effects on first render (X_API_KEY migration)
- See ClaudeCodeRenderer section above. Worth verifying that all production data is already migrated.

### Display defaults
- CLJS `claude_code_edit.cljs:24`: defaults Anthropic URL to `https://api.anthropic.com` when value empty.
- CLJS `connection_method.cljs:218`: defaults source selector placeholder to "Vault KV 1".

React relies on placeholder text only. Minor.

### AWS IAM Role connection method
- CLJS `process_form.cljs:113-138` (`is-aws-iam-role?` branch): writes `_aws_iam_rds:` prefix to USER/PASS and forces `PASS=authtoken`.
- React: `CONNECTION_METHODS.AWS_IAM` exists in `connectionPolicy.js` and `CredentialsTab.jsx` exposes the SelectionCard for postgres/mysql, but **no save-time encoding wires AWS IAM through.** `encodeSecretForSource` doesn't recognize the `aws-iam-role` source (`secretsCodec.js:43-47`). Picking AWS IAM mode currently does nothing on save.

---

## 5. Features que o React tem e o CLJS não

| Feature | Verdict |
|---|---|
| `UnsupportedFallback` (`CredentialsTab.jsx:72-89`) | **Intentional** — CLJS would render `nil` (line 33 in `credentials_tab.cljs`) when nothing matched. React shows a clear "not yet available" notice. **Improvement.** |
| `MetadataError` (`CredentialsTab.jsx:91-102`) | **Improvement.** CLJS silently renders nothing when metadata fails to load. |
| `dependsOnCatalog` loading gate (`L109-124, 134-137`) | **Improvement.** CLJS just renders an empty grid until metadata arrives. |
| `forceNewState` after method switch (`CredentialsTab.jsx:186-203`) | **Improvement.** CLJS resets `:metadata-credentials` per dispatch event; React's explicit `clearStagedSecrets` + `forceNewState` is the same intent, cleaner code path. |
| Per-row Cancel on staged rows (`EnvironmentVariablesSection.jsx:239-241`) | **Improvement.** CLJS just removes the row outright. |
| Reject unnamed placeholders at save (`store.js:490-507`) | **Improvement.** CLJS would persist junk like `filesystem:NEW_FILE_1`. |
| Auto-stage on source change in non-staged rows (`EnvironmentVariablesSection.jsx:233-235`, mirror in `PredefinedFields.jsx:113-123`) | **Improvement.** CLJS requires you to type something for the source to "stick". React makes the picker source-of-truth alone. |
| `secrets_updated_at` "Last updated" in `ReadOnlyStatus` | **Improvement.** New UI affordance. |
| Connection method `derivedMethod` from secret values (`utils/connectionMethod.js`) | **Equivalent** to CLJS `infer-connection-method` (`connection_method.cljs:60-84`). |

---

## 6. Comments que mentem / TODOs perdidos

### Verified accurate
- `secrets.go:82-87` lists the 5 round-trip shapes — matches `shouldRoundTripSecrets`.
- `renderers/index.jsx:9-20` dispatch-table preamble — accurate.
- `KubernetesTokenRenderer.jsx:24-37` Bearer prefix explanation — accurate.

### Comments worth tightening
- `CredentialsTab.jsx:174-177`: References `server.cljs:43`, `server.cljs:137`, `server.cljs:186`, `network.cljs:34/84`, `metadata_driven.cljs:121-139`, `claude_code_edit.cljs:59`. **Checked:**
  - `server.cljs:43` → `connection-method/main "custom"` ✓
  - `server.cljs:137` → `connection-method/main "ssh"` ✓
  - `server.cljs:186` → `connection-method/main "kubernetes-token"` ✓
  - `network.cljs:34` → `connection-method/main "httpproxy"` ✓
  - `network.cljs:84` → `connection-method/main "tcp"` ✓ (slightly off, the call is on `L84`)
  - `claude_code_edit.cljs:59` → `connection-method/main "claude-code"` ✓
  - All correct.

- `PredefinedFields.jsx:32-44` doc comment about round-trip: matches current backend (`secrets.go` `shouldRoundTripSecrets`). Accurate.

- `renderers/index.jsx:18-20`: "the catalog either omits them (claude-code, linux-vm, kubernetes-token) or doesn't capture the full shape they need" — accurate per `secrets.go:94-116`.

### Stale or misleading
- `webapp_v2/COMPONENTS.md:343` refers to `PredefinedFieldsCredentials.jsx` (old name; current name is `PredefinedFields.jsx`). **Stale doc reference**, not a code comment.
- `SshRenderer.jsx:32-35`: "clearing the opposite key is the save handler's responsibility (see store.save)" — actually it's now `deleteSecret()` in `handleAuthChange` (`L57-69`). The store's `save` doesn't have SSH-specific logic; this comment refers to where the staged delete *eventually* gets serialised. Wording could be clearer.
- `store.js:483-489`: comment refers to "renamed entries — even when stagedSecrets keeps the sentinel key — count as named". Correct, but the relationship between `renames` and `stagedSecrets` is the densest knot in the file. Worth one cleaner paragraph.

### TODOs / FIXMEs
None found in the audited files. Clean.

---

## 7. Top recommendations

| # | Priority | Change | Why | Size |
|---|----------|--------|-----|------|
| 1 | **High** | Either complete or hide AWS IAM Role mode. `CredentialsTab.jsx:177` exposes the card for postgres/mysql, but `encodeSecretForSource` (`secretsCodec.js:43-47`) doesn't emit `_aws_iam_rds:` and no save path applies the override of `PASS=authtoken`. Users can pick the option; nothing applies. | Silent functional gap; CLJS does this correctly (`process_form.cljs:113-138`). Either gate `supportsAwsIam()` to `() => false` until wired, or extend `encodeSecretForSource` + add the PASS override. | If wiring: ~30 LOC across `secretsCodec.js`, `KubernetesTokenRenderer`, `PredefinedFields`, store. If hiding: 1 LOC. |
| 2 | **High** | Add `field.description` rendering in `PredefinedFields.jsx` (passed to `SourcedInput` / `SecretField` as helper text). | Catalog schemas carry descriptions for AWS-* / kubernetes / redis. CLJS renders them (`metadata_driven.cljs:31`). React silently drops them — users lose context they used to have. | ~5 LOC + plumbing into `SourcedInput` if not already there. |
| 3 | **Medium** | Consolidate `dependsOnCatalog` and `buildRenderers` into one source of truth. Add `requiresCatalog: true/false` to each renderer entry; derive `dependsOnCatalog` by matching the entry. | Two places encode the same routing rule. Future shape additions risk drift (one updated, the other not). | ~15 LOC refactor, no behavior change. |
| 4 | **Medium** | Verify the Claude Code legacy `X_API_KEY` envvar migration is done server-side, or port the CLJS migration (`claude_code_edit.cljs:41-52`) into `ClaudeCodeRenderer`. | Connections still on the old shape will look broken in React. | Investigation; port is ~15 LOC. |
| 5 | **Medium** | Add `filesystem:SSH_PRIVATE_KEY` trailing-newline handling (`helpers.cljs:46-48`) somewhere — likely in `ConfigurationFilesSection.jsx` save path or `buildSecretsPatch`. | SSH agent may reject keys missing trailing `\n`. CLJS handles it transparently. | ~5 LOC. |
| 6 | **Low** | Either move `forceNewState` + `availableSources` to the store, *or* leave as props but document why (5-hop threading on stable props is workable). Recommend the latter — explicit beats clever. | Prop drilling is cosmetic; risk is when someone wants to read these from a deep component without re-threading. Not pressing. | If lifted: ~30 LOC moved into store + subscriptions. |
| 7 | **Low** | Decide on env-var key live-validation: either port CLJS's `valid-posix?` (`configuration_inputs.cljs:13`) to reject invalid keystrokes, or accept the looser blur-only UX as deliberate. Document the choice. | Today React allows `12_bad` keys at save time that the agent will reject. | ~10 LOC if adding live validation. |
| 8 | **Low** | Extract `decodeForDisplay(encoded)` helper (decode + strip provider prefix) used in `PredefinedFields.jsx:96-99, 140-143` + `EnvironmentVariablesSection.jsx:193-198` + `HttpHeadersSection.jsx:175-180` + `KubernetesTokenRenderer.jsx:62-66`. | Same 3-line dance copy-pasted in 4 spots. | ~8 LOC consolidation, 4 callsites simplified. |

### What's actively good (don't touch)
- `secrets.go::shouldRoundTripSecrets` and the comment at `L82-93` cleanly document the round-trip contract. The 5-shape list is the readable centerpiece of the refactor.
- The placeholder/rename/stagedSecrets contract in `store.js` is *the* most subtle piece of the page and the inline comments hold up.
- Hub-and-spoke layout — every renderer composes the same 3-5 shared sections — makes adding a new bespoke shape trivial (the work is a new file plus an entry in `index.jsx`).
- `cancelSecretChange` + `clearStagedSecrets` make method switching and per-field cancellation explicit and reversible, both of which CLJS handles with broader state resets.
