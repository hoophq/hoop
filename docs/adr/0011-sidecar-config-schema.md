# ADR-0011: Splitting `policy` into `guardrails` and `opa` in the sidecar config

- **Status:** Accepted
- **Date:** 2026-08-31
- **Author:** @matheusfrancisco
- **Code:** [`sidecar/daemon/config.go`](../../sidecar/daemon/config.go), [`sidecar/pii/alcatraz/`](../../sidecar/pii/alcatraz), [`sidecar/config/yaml/`](../../sidecar/config/yaml)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (request flow, current state), [ADR-0009](0009-guardrails-and-masking-architecture.md) (where these rules are evaluated; this ADR reverses its `pii.entities` requirement), [ADR-0012](0012-sidecar-enforcement-defaults.md) (the behaviour half of the same change)
- **Supersedes / Superseded by:** Supersedes [ADR-0006](0006-sidecar-config-defaults-and-overrides.md)

## Context

One word, `policy`, names two products in `config.yaml`. `policy.rules` is
Hoop's own rule set, evaluated in-process against a decoded statement.
`policy.opa` is a client for someone else's Rego. A reader cannot tell from the
key which of the two a line configures, and neither can a control-plane UI that
has to render both.

Five smaller frictions sit beside it, and all five cost an operator time on
their first config:

- `mask.rules[].entity` takes one entity. `policy.rules[].entities` takes a
  list. Both name the same alcatraz classes
  (`pii/alcatraz/masker.go:85`, `policy/policy.go:430`).
- `pii` must be declared before anything that detects can start. Six shipped
  configs carry the same hand-written list, four of them under the same copied
  comment. Omit the section and masking refuses to build
  (`daemon/daemon.go:590-592`).
- `listeners[].connection` and `listeners[].name` both name a lane.
  `displayName` falls back from one to the other
  (`daemon/daemon.go:379-385`), and seven of the eight configs in this repo
  set them to the same string.
- `mask.enabled` gates a list that already says whether it is empty. Worse, an
  absent or false `enabled` skips the whole validation block at
  `daemon/config.go:537-547`, so a lane can carry mask rules for a protocol
  that cannot mask and still load clean.
- `policy.enforce` reads like a global kill switch and gates only the evaluator
  chain. Masking, audit and the unswitchable denials all keep running without
  it. ADR-0012 covers that field.

One constraint shapes every option below. `LoadConfigBytes` decodes with
`DisallowUnknownFields` (`daemon/config.go:334`), and the YAML front end
transcodes to JSON and calls the same function
(`config/yaml/yaml.go:91-96`). A key that is not a declared Go struct field
fails the whole file. Deleting a field from the struct and keeping old configs
working are therefore mutually exclusive, and no leftover-map escape hatch
exists.

## Options considered

1. **Rename in place and break old configs.** One release, one schema, no
   compat code. Rejected: every running deployment fails to start on upgrade,
   and the failure arrives as `unknown field "policy"` with no instruction.
2. **Accept both spellings forever.** No migration pressure and no removal
   work. Rejected on the evidence of `hoop start inspect`, deprecated in
   1.149.0 with the message "removed in a future release"
   (`client/cmd/startsidecar.go:96-105`) and still shipping. Six permanent
   aliases would leave the schema more complicated than the one this ADR
   simplifies.
3. **A two-spelling window with a single normalize step and a named removal
   release.** Both spellings load, the old one warns, and one function knows
   the mapping so nothing downstream carries compat logic. Chosen.

## Decision

We split the section by product and drop the fields that restate what a list
already says.

| Today | New | Note |
|---|---|---|
| `policy.rules` | `guardrails.rules` | shape unchanged, all 8 rule types |
| `policy.enforce` | `guardrails.mode` | `enforce` \| `observe`; see ADR-0012 |
| `policy.opa.*` | `opa.*` | top level and per listener |
| `mask.rules[].entity: X` | `mask.rules[].entities: [X]` | list only, no scalar form |
| `mask.enabled` | removed | rules present means masking on |
| `listeners[].connection` | removed | `name` fills the audit key |
| `pii.entities` required | optional | omitting the section enables all 54 |

We keep `guardrails` as the name even though the gateway already ships a
feature under that word. The two engines stay separate, as ADR-0009 decided,
but their rule-type strings are identical rather than similar:
`deny_words_list` and `pattern_match` appear verbatim in
`gateway/guardrails/guardrails.go:12-15` and `policy/policy.go:315,318`. The
sidecar set is a superset of the gateway set, so one word over both is a
promise the sidecar can keep. The alternatives lose on their own terms:
`policy` is the word being freed for OPA, and `rules` cannot survive a UI.

### One funnel for compatibility

```
LoadConfigBytes
  → json.Decode with DisallowUnknownFields      unchanged
  → cfg.normalize()                             NEW: fold deprecated → canonical, collect warnings
  → cfg.Validate()                              unchanged, reads canonical fields only
```

`normalize` is the only function that knows a deprecated field exists.
`resolve`, `validateLane`, `buildPolicy` and `buildMasker` read the canonical
shape. Without the funnel the compat logic smears across five functions and two
modules.

Setting both spellings at one scope refuses the config. Picking a winner
silently would contradict the rule that already governs this decoder: "a typo
in a key must not silently disable a control" (`daemon/config.go:334`).

Presence detection makes three fields pointers. `Config.Policy` and
`Config.Mask` are value types today (`daemon/config.go:34,38`), so absent and
empty are indistinguishable, and `AuditConfig.FailClosed` has the same problem
for the polarity flip in ADR-0012. All three become pointers before any of the
rest lands.

### Omitting `pii` enables everything

Six code locations and four config comments argue that `pii.entities` is
required because turning on all recognizers corrupts ordinary data, citing a
measured 32% false-positive rate for `US_SSN` on nine-digit business ids
(`pii/alcatraz/alcatraz.go:33-41`, `:80-107`, `:110-126`).

The measurement is right. The conclusion does not reach the case of an omitted
section, for two reasons found in the code that does the scanning:

- `NewMasker` narrows the engine to exactly the entities its own rules name:
  `opts.Entities = entities` at `pii/alcatraz/masker.go:249-250`. A permissive
  detector with 54 entities active, driving a masker with two rules, scans for
  two. The `order_id` corruption in the docs needs someone to write a `US_SSN`
  mask rule, which is an explicit act either way.
- `matchesPII` intersects the scanner output with the rule's own `Entities`
  (`policy/pii.go:83-86`), and the finding published to OPA carries the
  intersection (`policy/policy.go:618,623-625`). A permissive detector reports
  no entity class a rule did not ask about.

One real cost survives. `ScanText` runs over the detector's full active set
(`pii/alcatraz/alcatraz.go:317-337` reading `d.opts`), so a permissive default
would run 54 recognizers per statement instead of the five a typical config
names, then discard the extras. We narrow `ScanText` to the union of entities
named by the lane's `pii` rules, mirroring what `NewMasker` already does for
the response path. The two changes ship together.

`AllEntities()` returns 54: 51 alcatraz built-ins plus `AWS_ACCESS_KEY`, `JWT`
and `PRIVATE_KEY` from `pii/alcatraz/secrets.go:23-27`. The figure "45" in six
code and doc locations is stale; ADR-0009 already carried the correct count.
`pii.ignored` (`pii/alcatraz/alcatraz.go:128-130`) becomes the recommended
knob, subtracting the seven recognizers listed in `alcatraz.Noisy`.

### The merge table, restated

Mechanics unchanged from ADR-0006. The keys change, and the split makes the
per-field rules legible: two sections with two behaviours instead of one
section with three.

| Field | Global → lane | Why |
|---|---|---|
| `guardrails.rules` | **concatenate**, lane's first | Every local rule denies or defers and the first match wins, so concatenating is monotonic in the allow/deny outcome. Order picks which name and message the user reads. `rules: []` opts the lane out entirely. |
| `guardrails.mode` | **replace** when set | A lane rolling out behind an enforcing default has to be able to say observe. |
| `opa` | **replace** when set | One lane has one decision endpoint. |
| `mask.rules` | **replace** when set | A rule owns an entity or a column. Concatenating leaves two rewrites of one value. |
| `pii`, `analyzer`, `audit`, `admin` | process-wide | One detector engine and one analyzer per process. |

Each of the three sections a lane can override also has a spelling for
wanting none of it, and they are the same shape on purpose:

| Section | Opt out with | Reads as |
|---|---|---|
| `guardrails` | `guardrails: {rules: []}` | no rules on this lane |
| `opa` | `opa: {}` | no decision endpoint on this lane |
| `mask` | `mask: {rules: []}` | no rewriting on this lane |

All three hang on the decoder distinguishing an empty collection from an absent
key. `mask.rules` is a `json.RawMessage`, so `[]` arrives as two bytes rather
than as nil. `guardrails.rules` is a `[]policy.Rule`, and `[]` decodes to a
non-nil slice of length zero, which is why the merge reads presence rather than
length: `len(o.Rules) > 0` would read the opt-out as silence and hand the lane
the defaults it just refused. An empty list meaning "adds nothing" is the other
defensible reading for a field that concatenates, and it would leave a lane no
way to say the thing at all. Tests pin each one, because the distinction is
invisible in the type.

### Startup refusals, restated

| Config | Refused unless |
|---|---|
| `opa.gate: true` | the lane has at least one `ai_analysis` rule |
| an `ai_analysis` rule with no `trigger:` | the lane has `opa.gate: true` |
| any `ai_analysis` rule | the config has an `analyzer:` section, the protocol has a content builder, and on HTTP `http.capture_body: true` |
| `mask.rules` non-empty | the protocol supports masking (`gate.MaskSupported`) |
| both spellings of one field | never; the config is refused |

Two rows leave the table. `mask.enabled: true` with empty rules stops being
expressible. A rule with `action: defer` and no OPA stops being a refusal and
starts denying, which ADR-0012 explains.

## Consequences

**One shipped behaviour reverses, and it needs release-note prose.** A lane
with `mask.enabled: true` and no `pii` section refuses to start today
(`daemon/daemon.go:590-592`) and will start and mask, because a detector now
always exists.

**No deployment stops booting over masking.** Dropping `mask.enabled` makes the
protocol check at `daemon/config.go:537` run on every lane that carries rules,
where the flag used to skip it. That reads like a new way to fail an upgrade
and is not one: `gate.MaskSupported` asks the codec for a `Reframer` rather
than listing protocol names (`gate/gate.go:544-554`), and all three shipped
codecs have one, HTTP through Content-Length re-tagging. Verified by loading a
config with mask rules on postgres, mssql and http lanes: all three validate
clean and report `+ masking`. The check earns its place as the guard for a
future codec that relays without re-framing, not as a migration hazard.

**`analyzer.send: redacted` and `refuse` start working without a `pii`
section.** The refusal at `daemon/analyzer.go:206-212` tests for a section that
now always resolves to a detector.

**The audit key follows `name` instead of `connection`.** Seven of the eight
configs in this repo set them to the same value, so nothing moves.
`metabase-stack/sidecar/config.yaml:40,44` sets `name: appdb` and
`connection: metabase`, which is the one in-repo demonstration of why the field
existed: the listener is named for the upstream and the connection for the
consumer. That stack's audit rows change key, and
`envoy-stack/sidecar/read-audit.py:42` prints the new value. Operators whose
two fields differ rename the listener before upgrading, or their audit history
splits across two keys.

**The OPA input document does not move.** All 13 fields at
`policy/opa.go:106-137` stay byte-identical, `input.phase` derives from Go
constants rather than from config (`policy/opa.go:86,89`), and
`input.context.connection` keeps its key while changing which config field
fills it. No Rego policy needs an edit. `session/session_test.go:108` pins the
context shape as a public contract and keeps passing.

**Every inheritable section gains an opt-out, which two of them lacked.** A
top-level `opa` used to reach every lane with no escape: `resolve` replaces only
when the listener sets one, and `opa: {url: ""}` is refused. `guardrails.rules`
had the same hole, and `mode: observe` did not fill it, since an observing lane
still evaluates and evaluating is what costs money on a lane with `ai_analysis`
rules. Both are closed by the table above, and closing them was cheap while the
sections were being rewritten anyway. `opa: {}` is read as the opt-out only
when every field in the block is zero; a block naming a timeout with no url is
still refused, because that configures a client that cannot be built.

**Deprecated fields stay in the Go structs until a named release removes
them.** Constraint C from the Context leaves no alternative. The window runs
two minor releases warning, one refusing with instructions, then removal.
`hoop-inspect -validate` gains `-strict`, which exits non-zero on a
deprecation so a team can fail its own pipeline before the release that fails
it for them.

**Documentation drifts in fifteen files at once.** `sidecar/README.md` carries
the reference (L201-620 and L922-1260), and ADR-0005 restates the merge and
refusal tables at L267-271 and L1194-1237. The migration table lands at
README L207, before the first example, and the blockquote at README L3-4
promising the schema will hold gets rewritten.

### Migration

| Old | New | While deprecated | On removal |
|---|---|---|---|
| `policy.rules` | `guardrails.rules` | works, warns | — |
| `policy.enforce: true\|false` | `guardrails.mode: enforce\|observe` | works, warns | see ADR-0012 |
| `policy.opa.*` | `opa.*` | works, warns | — |
| `mask.enabled: true` | drop the key | works, warns | — |
| `mask.enabled: false` + rules | drop the rules from that scope | **stays authoritative**, masking stays off, warns | masking turns **on** |
| `mask.rules[].entity: X` | `entities: [X]` | works, warns | — |
| `listeners[].connection` | `listeners[].name` | maps to `name` when `name` is empty, warns | audit key follows `name` |
| `pii` omitted | still omitted | — | detector gains all 54 entities |

## Worked example

Five lanes, all eight rule types, both protocols that mask, one that does not.

```yaml
log_level: info
admin: {listen: "127.0.0.1:9090"}

audit:
  file: "-"
  async_queue_size: 1024
  query_sessions: 500
  fail_open: false                # renamed and reversed; see ADR-0012

pii:
  # Omit this section and all 54 entity types are active. It exists to
  # subtract: these six are the recognizers that fire on ordinary business
  # data. US_SSN is absent because this deployment holds real ones.
  ignored: [URL, DATE_TIME, ABA_ROUTING, AU_TFN, AU_ACN, US_ITIN]
  allow_list: ["4111111111111111"]

analyzer:
  provider: anthropic
  model: claude-sonnet-4-5
  fail_open: true                 # the one deliberate fail-open; see ADR-0012
  send: redacted
  cache: {size: 4096, ttl_sec: 900}

guardrails:
  mode: enforce
  rules:
    - name: no-schema-changes
      type: operation
      operations: [drop, truncate, alter]
      message: schema changes go through migrations

    - name: customers-is-crm-owned
      type: table
      tables: [customers]
      access: write               # writes only, not every mention
      require_table_match: true   # a statement whose tables could not be read also denies
      message: customers is owned by the CRM; write through it

    - name: no-server-file-access
      type: deny_words_list
      words: [pg_read_file, pg_ls_dir, lo_import, lo_export]

    - name: no-unbounded-delete
      type: pattern_match
      pattern_regex: '(?i)\bdelete\s+from\s+\w+\s*;?\s*$'

    - name: no-identifiers-in-query
      type: pii
      entities: [US_SSN, CREDIT_CARD, BR_CPF, IBAN_CODE]
      action: defer               # denies on a lane with no opa; see ADR-0012
      message: a national identifier in a query lands in the database's own log

mask:
  rules:
    - {name: contact-details, entities: [EMAIL_ADDRESS, PHONE_NUMBER], strategy: redact}
    - {name: card-numbers,    entities: [CREDIT_CARD], strategy: partial, keep_last: 4}
    - {name: national-ids,    entities: [US_SSN, BR_CPF], strategy: hash}
    - {name: risk-columns,    columns: [risk_score], strategy: redact}

listeners:
  # Inherits everything. No OPA, so the deferring rule above denies.
  - name: appdb
    protocol: postgres
    listen: "127.0.0.1:5433"
    upstream: "db.internal:5432"
    upstream_tls: {ca_file: /etc/hoop/ca.pem, server_name: db.internal}
    downstream_tls: {cert_file: /etc/hoop/relay.crt, key_file: /etc/hoop/relay.key}

  # Dry run over the same rules. See ADR-0012.
  - name: reporting
    protocol: postgres
    listen: "127.0.0.1:5434"
    upstream: "warehouse.internal:5432"
    guardrails: {mode: observe}
    mask: {rules: []}             # the empty list opts out of the inherited set

  # MSSQL masks: its codec re-frames, so this lane could carry mask rules.
  # It opts out to show the spelling, since `rules: []` is the only way a lane
  # refuses a set the top level configured.
  - name: mssqldb
    protocol: mssql
    listen: "127.0.0.1:1434"
    upstream: "mssql.internal:1433"
    guardrails:
      rules:                      # concatenates, this lane's first
        - {name: no-xp-cmdshell, type: deny_words_list, words: [xp_cmdshell, sp_OACreate]}
    mask: {rules: []}

  # HTTP with the analyzer and a two-phase gate.
  - name: api
    protocol: http
    listen: "127.0.0.1:8081"
    upstream: "orders.internal:8080"
    identity_header: x-hoop-user
    http: {capture_body: true, max_body_bytes: 65536}
    opa:
      url: http://opa:8181/v1/data/hoop/inspect/result
      fail_open: false
      gate: true                  # ask Rego whether a model call is worth it
    guardrails:
      rules:
        - {name: no-admin-api, type: http_resource, resources: ["/admin/**"]}
        - {name: leaking-server-errors, type: http_status, statuses: ["5xx"]}
        - name: risky-payloads
          type: ai_analysis
          trigger: {methods: [POST, PUT], resources: ["/orders/**"]}
          high: defer
          medium: warn
          low: allow
    mask:
      rules:                      # replaces the default set
        - {name: response-emails, entities: [EMAIL_ADDRESS], strategy: redact}
        - {name: credentials, entities: [AWS_ACCESS_KEY, JWT, PRIVATE_KEY], strategy: redact}

  # Filesystem permissions decide who reaches this one.
  - name: internal-jobs
    protocol: postgres
    network: unix
    listen: /run/hoop/jobs.sock
    upstream: "db.internal:5432"
```

Resolved:

| Lane | mode | guardrail rules | OPA | masking |
|---|---|---|---|---|
| `appdb` | enforce | 5 inherited | none | 4 rules |
| `reporting` | observe | 5 inherited, evaluated, none enforced | none | off |
| `mssqldb` | enforce | 1 own + 5 inherited, own first | none | off, by `rules: []` |
| `api` | enforce | 3 own + 5 inherited | gate and decide | 2 rules |
| `internal-jobs` | enforce | 5 inherited | none | 4 rules |

`no-identifiers-in-query` shows the split. On `appdb` the match denies, because
nothing can rule on the finding. On `api` the same rule reports
`input.findings.pii.values.entities` and Rego decides. One file, deployed with
or without OPA, safe at both ends.

## How this will be verified

- `hoop-inspect -validate` against all six shipped configs in the deprecated
  spelling (loads, warns) and the new spelling (loads, silent).
- A config setting both spellings of every field that has two, which must
  produce every refusal in one run. Four qualify: `policy.rules`,
  `policy.enforce`, `policy.opa` and `audit.fail_closed`. The other three
  changes fold with no conflicting pair to write.
- `make test-sidecar`, which walks every `go.mod` under `sidecar/`.
- `TestOPAInputDocumentShape` (`policy/policy_test.go:308-351`) and
  `policy/evalcontext_test.go:67-256`, unchanged and passing, proving the Rego
  contract held.
- `deploy/docker-compose/metabase-stack/demo.sh`, the only end-to-end assertion
  over `mask.rules` against a real result set.
- A new test for `PluginFromConfig` (`pii/alcatraz/config.go`), which has no
  coverage today and owns the omitted-section branch this ADR changes.
