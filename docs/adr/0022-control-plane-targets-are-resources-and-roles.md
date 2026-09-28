# ADR-0022: Control plane targets are resources, listeners and roles, not a JSON document

- **Status:** Proposed
- **Date:** 2026-09-28
- **Author:** @rogerio
- **Deciders:** @rogerio
- **Related:** [ADR-0013](0013-gateway-control-plane-mode.md) (control-plane mode), [ADR-0017](0017-control-plane-composes-sidecar-rules.md) (composed rules), [ADR-0019](0019-sidecar-config-file-rules-become-rule-items.md) (file rules become rule items), [ADR-0020](0020-control-plane-reviewers-from-the-identity-provider.md), [ADR-0021](0021-review-feedback-for-agents.md)
- **Supersedes / Superseded by:** reverses the direction of migration 000116; amends ADR-0017 and ADR-0019

## Context

### What the control plane stores today

- `private.sidecars.configuration` is a JSONB copy of the daemon config
  (000116). Listeners are elements of that document, not rows.
- Everything that targets a listener keys on `(sidecar_id, listener_name)`
  with no foreign key: `guardrail_rules_listeners`,
  `datamasking_rules_listeners`, `ai_session_analyzer_rules_listeners`
  (000120), `sidecar_slack_channels` (000123), and
  `reviews.listener_name` (000117). The migrations say why:
  "listener_name is NOT a foreign key and cannot be: a listener is an element
  of sidecars.configuration, not a row."
- Integrity lives in code: `services.ValidateListenerNames`, the bind-time
  presence check, and per-table pruning on every configuration write
  (`models.PruneSidecarSlackChannels`).
- Footprint: 17 non-test files in `gateway/` reference `listener_name` /
  `ListenerName` (98 references), plus 10 test files.

### The same model exists twice

The gateway already binds rules to targets with real foreign keys:
`guardrail_rules_connections`, `datamasking_rules_connections` (by
`connection_id`), and `*_rules_attributes` + `connections_attributes` (by
name, `ON UPDATE CASCADE`). The `*_rules_listeners` tables are the same shape
with `(sidecar_id, listener_name)` in place of the connection. Each new
feature on the control plane adds one more listener-keyed table.

### What is coming

The legacy features the control plane must gain (JIT, access control,
runbook rules, access-request rules, session view) are all keyed on
`private.connections` / `private.resources` in the gateway. On today's model
each needs a new listener-keyed table, a new validator and a new prune step.

### Constraints

- The sidecar does not change. It keeps receiving the same document on
  `POST /api/sidecars/handshake`. The served shape is the contract.
- The sidecar governs connections; it does not originate them. A role is
  *who may pass through a lane*, not a credential the control plane uses to
  execute. Database passwords stay with the client.
- The gateway ↔ agent contract does not change.
- Work already shipped (000114 to 000124, the Sidecars pages, rule binding,
  reviews) must migrate, not be rewritten.

## Options considered

1. **Keep the JSON document (status quo).** No migration. Loses: every new
   feature repeats the listener-keyed table + validator + prune pattern, and
   none of the gateway's connection-keyed features can be reused.
2. **Normalize listeners only** (`sidecar_listeners` rows, junctions get a
   real FK). Small and safe. Loses on its own: still a second target model
   beside resources/connections, so JIT, ACL and runbook rules still need new
   tables. It is kept as step 1 of the chosen option.
3. **Project listeners into `connections` as derived rows**
   (`managed_by='sidecar'`, written on each configuration save). Reuses
   connection-keyed code at once. Loses: two sources of truth (the JSON and
   the projection) that must be kept in sync on every write, and
   `PUT /connections` already rewrites `managed_by` (`gateway/api/connections/connections.go:206-218`).
4. **Resources, listeners and roles are the source of truth; the served
   document is composed from them.** Chosen, in stages.

## Decision

We store what a sidecar serves as rows, and compose the document at serve
time, as ADR-0017 already does for rules.

### Schema

```
sidecars           id, org_id, name, key_hash, process config (admin, audit,
                   log_level, top-level analyzer), handshake state
resources          existing table; managed_by='sidecar', agent_id NULL,
                   type = the control-plane protocol
sidecar_listeners  id, org_id, sidecar_id FK ON DELETE CASCADE,
                   resource_id FK ON DELETE RESTRICT, name, protocol, listen,
                   network, upstream, upstream_tls, downstream_tls,
                   identity_header, limits, protocol_opts JSONB,
                   UNIQUE (sidecar_id, name)
connections        existing table used as a ROLE of the resource: the database
                   principal allowed through the lane (pg/mysql/mssql user, ssh
                   principal). No credentials: the client authenticates end
                   to end. managed_by='sidecar', agent_id NULL.
```

- `managed_by='sidecar'` marks a resource or role as control-plane owned.
  Gateway pages and forms filter it out.
- `type` holds the control-plane protocol (postgres, mysql, mssql, http, grpc,
  ssh, mongodb, clickhouse, spanner). It selects the edit form. Control-plane
  types are not gateway connection types, and gateway code must not branch on
  them.

- **Resource** is what the listener protects. One resource may sit behind
  several listeners (two regions, two sidecars). Replicas of one deployment
  share a token, so they are one sidecar row.
- **Listener** is a row, with foreign keys to its sidecar and its resource.
- **Role** carries access control, JIT, runbook rules and access-request
  rules, as in the gateway. HTTP and gRPC listeners may have no role.

### Rule binding

Rules bind to the **resource**. `*_rules_listeners` becomes
`*_rules_resources`, with a foreign key to `resources`.

The sidecar loads a lane's rules when it accepts the connection
(`sidecar/proxy/proxy.go:378`), before it knows the database user. So a lane
enforces one rule set. A resource is exactly what its listeners protect, so a
resource binding has the same granularity as today's listener binding. No
write can express a rule set the sidecar cannot enforce.

Rules per role need the sidecar to pick rules after the startup message. That
is a sidecar change and a later ADR. The role-level junctions
(`*_rules_connections`, `*_rules_attributes`) stay gateway-only until then.

### Composition and import

- `services.ComposeSidecarConfiguration` builds `listeners[]` from
  `sidecar_listeners` + `resources` + bound rules instead of reading
  `configuration.listeners`. The output document is byte-compatible with
  today's, so `CheckServable`, capability gating and the sidecar are
  untouched.
- The first-boot import (`PUT /api/sidecars/configuration` after a 412)
  splits the file: each listener becomes a `sidecar_listeners` row plus a
  `resources` row (matched by name, created if absent), and rules become rule
  items as ADR-0019 already does. The import still runs only into an empty
  plane.
- `GET /api/sidecars/configuration` keeps returning the composed document.

### Reviews and Slack channels

`reviews` and `sidecar_slack_channels` gain `sidecar_listener_id` with a real
FK. `listener_name` stays readable during the transition so an old sidecar's
review filing (which names the listener) resolves to the row.

## Prototype

A first cut runs behind `experimental.sidecar_resources` (default off), on
reviews only. It maps **sidecar = resource, listener = role**: each sidecar is
a `resources` row and each listener a `connections` row under it, both
`managed_by='sidecar'` with `agent_id` NULL. Resource and role are storage
only; the UI, the handshake, the first-boot import and the Sidecars pages
still speak sidecar and listener, and `sidecars.configuration` stays the
source of the served document.

- Migration 000125 adds `resources.managed_by`, `sidecars.resource_name` (FK
  to `resources`) and `sidecar_listener_roles` (listener to role, FKs on both
  sides).
- `services.ProjectSidecarTx` writes the rows inside every configuration write
  (create, PUT, PATCH, import) and removes the roles of dropped listeners. A
  row with the same name and another owner is refused with 422, never adopted.
- A new sidecar review points `connection_name`/`connection_id` at the
  listener's role, and `GET /reviews` reports `connection_name` and
  `resource_name`. Dedupe and claim still key on `(sidecar_id,
  listener_name)`.
- A test pins that the served revision does not change when the rows are
  written.

This does not yet move composition or rule bindings onto the rows (steps 1 to
3 below).

## Migration plan

Each step ships on its own and leaves the system consistent.

1. **Listener rows.** Create `sidecar_listeners`; backfill from
   `jsonb_array_elements(configuration->'listeners')`; add
   `sidecar_listener_id` to the four junction tables and `reviews`; backfill;
   compose reads rows. The JSON `listeners` key is kept but no longer read.
   Gain: real FKs, `ValidateListenerNames` and prune code can go.
2. **Resources.** Create one `resources` row per listener
   (`managed_by='sidecar'`, `agent_id` NULL) and set
   `sidecar_listeners.resource_id`. Name collisions with gateway resources in
   the same org are resolved by suffix, reported in the migration log, never
   merged silently.
3. **Rule junctions.** Copy each `*_rules_listeners` row to
   `*_rules_resources` on the listener's resource. Retire `*_rules_listeners`.
   Drop `configuration.listeners`.
4. **Roles and features.** Roles, access control, JIT, access-request and
   runbook rules, reusing the gateway handlers. Out of scope of this ADR's
   migration; each feature states its own design.

### No sidecar sees the migration

The served revision is the SHA-256 of the composed document
(`configRevision`, `gateway/api/sidecar/sidecar.go:49`). A migration that
composes a byte-equal document keeps the revision, so a running sidecar sees
no drift, reloads nothing and drops no connection.

This is enforced, not assumed:

- Each step's migration test composes every sidecar before and after and
  requires equal revisions. It covers every fixture in
  `gateway/services/sidecarconfig_test.go`.
- The Go migration that runs the backfill does the same check per sidecar at
  startup. A sidecar whose revision would change fails the migration and stops
  startup. It is never served a different document.
- Every step has `.up.sql` and `.down.sql`.

## Consequences

**Easier**

- One target model for gateway and control plane. JIT, ACL, access-request
  and runbook rules can reuse gateway code instead of adding
  listener-keyed tables.
- Integrity moves from code to foreign keys. Removing a listener with
  bindings fails in the database, not in a validator someone must remember.
- The UI can reuse the resource and role pickers and pages.
- Structural work stays where it is needed: the transport (gRPC) and the
  plugin chain, not the data model.

**Harder**

- Four migrations with backfills over live data, and rollback for each.
- Compose becomes a join instead of a copy; its test suite must pin
  byte-equal output.
- The import path gains a resource match/create step with name-collision
  rules.
- The Sidecars pages in `webapp_v2` read listeners from the document today;
  they move to listener endpoints.
- A control plane that shares its database with a gateway (ADR-0013) now
  shares `resources` and `connections` rows too. Access control on those rows
  applies in both.

**Unchanged**

- The sidecar binary, its config schema and the handshake wire contract.
- The gateway ↔ agent contract.

**Open questions**

- `*_rules_resources` adds three junction tables beside the gateway's
  role-level ones. Could the gateway's `*_rules_attributes` path (attributes
  on resources) replace them instead?
- Where does the backfill's byte-equality check run for a control plane that
  shares its database with a gateway (ADR-0013)?

**Revisit** if step 1 alone removes enough cost that steps 2 and 3 are not
worth their migrations. Step 1 is useful without them.
