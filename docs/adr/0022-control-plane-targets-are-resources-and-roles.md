# ADR-0022: The control plane stores sidecars as resources and listeners as roles

- **Status:** Proposed (revised 2026-09-29 after the prototype; see "Prototype")
- **Date:** 2026-09-28
- **Author:** @rogerio
- **Deciders:** @rogerio
- **Related:** [ADR-0013](0013-gateway-control-plane-mode.md) (control-plane mode), [ADR-0017](0017-control-plane-composes-sidecar-rules.md) (composed rules), [ADR-0019](0019-sidecar-config-file-rules-become-rule-items.md) (file rules become rule items), [ADR-0020](0020-control-plane-reviewers-from-the-identity-provider.md), [ADR-0021](0021-review-feedback-for-agents.md)
- **Supersedes / Superseded by:** reverses the direction of migration 000116; amends ADR-0017 and ADR-0019

> **Revision (2026-09-29).** The first draft stored one resource per listener
> and bound rules to resources. The prototype chose, and validated, **sidecar
> = resource, listener = role**. A role is then exactly one lane, so rules
> bind to roles through the gateway's own junctions and no new rule table is
> needed. This version records that.

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
- Integrity lives in code: `services.ValidateListenerNames`,
  `services.ValidateSidecarBindingsForConfiguration`, and per-table pruning
  on every configuration write (`models.PruneSidecarSlackChannels`).
- Footprint: 17 non-test files in `gateway/` reference `listener_name` /
  `ListenerName` (98 references), plus 10 test files.

### The same model exists twice

The gateway already binds rules to roles (connections):
`guardrail_rules_connections` and `datamasking_rules_connections` by
`connection_id` with `ON DELETE CASCADE` (000025, 000038),
`ai_session_analyzer_rules.connection_names` (000066), and
`*_rules_attributes` + `connections_attributes`. The `*_rules_listeners`
tables are the same shape with `(sidecar_id, listener_name)` in place of the
connection. Each new control-plane feature adds one more listener-keyed table.

### What is coming

The legacy features the control plane must gain (JIT, access control,
runbook rules, access-request rules, session view) are all keyed on
`private.connections` / `private.resources`. On today's model each needs a
new listener-keyed table, a new validator and a new prune step.

### Constraints

- The sidecar does not change. It keeps receiving the same document on
  `POST /api/sidecars/handshake`. The served shape is the contract.
- The sidecar governs connections; it does not originate them. A role is
  *what a client may reach through a lane*, not a credential the control
  plane uses to execute. Database passwords stay with the client.
- A control plane never shares its database with a gateway.
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
   tables.
3. **One resource per listener**, rules bound to the resource (the first
   draft of this ADR). Loses: it needs three new `*_rules_resources` tables,
   and the gateway's role-level junctions stay unused.
4. **Sidecar = resource, listener = role**, rows as the source of truth and
   the served document composed from them. Chosen. It mirrors the gateway (a
   resource with several roles), and a role has exactly the granularity of a
   lane, so the existing role-level rule junctions apply as they are.

## Decision

We store a sidecar as a resource and each of its listeners as a role, and
compose the served document from those rows, as ADR-0017 already does for
rules. Resource and role are storage only: the UI, the handshake, the
first-boot import and the Sidecars pages keep saying sidecar and listener.

### Schema

```
resources          the sidecar: name = sidecar name, type 'custom',
                   subtype 'sidecar', managed_by 'sidecar', agent_id NULL
sidecars           id, org_id, name, key_hash, resource_name FK -> resources,
                   process config (admin, audit, log_level, top-level
                   analyzer), handshake state
connections        the listener's role: name '<sidecar>.<listener>',
                   resource_name = the sidecar's resource, type/subtype from
                   the protocol, managed_by 'sidecar', agent_id NULL, no
                   credentials
sidecar_listeners  1:1 with its role: connection_id FK ON DELETE CASCADE,
                   sidecar_id FK ON DELETE CASCADE, listener name, protocol,
                   listen, network, upstream, upstream_tls, downstream_tls,
                   identity_header, limits, protocol_opts JSONB,
                   UNIQUE (sidecar_id, name)
```

- `managed_by='sidecar'` marks a row as written by the control plane from a
  sidecar. A row of another owner with the same name is refused, never
  adopted.
- The role `type`/`subtype` selects the edit form. Control-plane protocols
  (http, grpc, ssh, clickhouse, spanner) map to `custom/<protocol>`;
  postgres, mysql, mssql and mongodb map to `database/<protocol>`. Gateway
  code must not branch on them.
- Role names are `<sidecar>.<listener>`, because connections are unique per
  org and listeners only per sidecar. The name must pass
  `ValidateResourceName`.

### Rule binding

Rules bind to the **role** through the gateway's junctions:
`guardrail_rules_connections`, `datamasking_rules_connections`, and
`ai_session_analyzer_rules.connection_names`. `*_rules_listeners` is retired.

The sidecar loads a lane's rules when it accepts the connection
(`sidecar/proxy/proxy.go:378`). One role is one lane, so a role binding has
exactly the granularity the sidecar enforces. No write can express a rule set
it cannot apply.

### Composition and import

- `services.ComposeSidecarConfiguration` builds `listeners[]` from
  `sidecar_listeners` + roles + bound rules instead of reading
  `configuration.listeners`. The output is byte-equal to today's, so
  `CheckServable`, capability gating and the sidecar are untouched.
- The first-boot import (`PUT /api/sidecars/configuration` after a 412)
  writes the resource, one role and one `sidecar_listeners` row per listener,
  and turns rules into rule items bound to those roles, as ADR-0019 already
  does. It still runs only into an empty plane.

### Reviews and Slack channels

- A sidecar review points `connection_name` / `connection_id` at the
  listener's role (done in the prototype). Dedupe and claim keep keying on
  `(sidecar_id, listener_name)`, which an old sidecar's filing still names.
- `sidecar_slack_channels` moves to the role with a foreign key.

## Prototype

Behind `experimental.sidecar_resources` (default off), reviews only.
Migration 000125 adds `resources.managed_by`, `sidecars.resource_name` and
`sidecar_listener_roles` (listener to role, FKs on both sides).
`services.ProjectSidecarTx` writes the rows inside every configuration write
(create, PUT, PATCH, import). `sidecars.configuration` stays the source of
the served document.

Validated end to end on a running control plane with a real sidecar:

| Case | Result |
|---|---|
| Sidecar becomes a resource, each listener a role | rows written, `managed_by='sidecar'`, `agent_id` NULL |
| A held statement files a review | review and session point at the role; `GET /reviews` reports `connection_name` and `resource_name`; approve and claim reach EXECUTED |
| A listener is removed | its role and mapping are deleted |
| The sidecar is deleted | resource, roles and mapping are deleted |
| The sidecar sees none of it | the served revision does not change (pinned by a test) |

The test also showed the friction this ADR removes. Removing the `reporting`
listener was refused until two rules (a masking rule and an analyzer rule)
were unbound by hand, one `PUT` each, because `*_rules_listeners` carries no
foreign key and the check lives in code.

## Migration plan

Each step ships on its own and leaves the system consistent.

1. **Resource and roles** (done, behind the flag).
2. **Listener rows.** `sidecar_listener_roles` becomes `sidecar_listeners`
   and carries the listener's fields; compose reads rows. The JSON
   `listeners` key is kept but no longer read.
3. **Rule junctions.** Copy each `*_rules_listeners` row to the role-level
   junction of its kind, and move `sidecar_slack_channels` to the role.
   Retire `*_rules_listeners` and `ValidateListenerNames`. Drop
   `configuration.listeners`.
4. **Features.** Access control, JIT, access-request and runbook rules on
   roles, reusing the gateway handlers. Out of scope here; each feature
   states its own design.

### No sidecar sees the migration

The served revision is the SHA-256 of the composed document
(`configRevision`, `gateway/api/sidecar/sidecar.go:49`). A migration that
composes a byte-equal document keeps the revision, so a running sidecar sees
no drift, reloads nothing and drops no connection.

This is enforced, not assumed:

- Each step's test composes every sidecar before and after and requires
  equal revisions, over every fixture in
  `gateway/services/sidecarconfig_test.go`.
- The Go migration that runs a backfill does the same check per sidecar at
  startup. A sidecar whose revision would change fails the migration and
  stops startup. It is never served a different document.
- Every step has `.up.sql` and `.down.sql`.

## Consequences

**Easier**

- One target model for gateway and control plane. JIT, ACL, access-request
  and runbook rules reuse gateway code instead of adding listener-keyed
  tables.
- Integrity moves from code to foreign keys. What happens to a removed
  listener's bindings is a database rule, not a sequence of manual `PUT`s.
- Rule binding reuses the gateway's role-level junctions; no new rule table.
- The UI can reuse the resource and role pickers and pages.
- Structural work stays where it is needed: the transport (gRPC) and the
  plugin chain, not the data model.

**Harder**

- Backfills over live data in steps 2 and 3, with rollback for each.
- Compose becomes a join instead of a copy; its tests must pin byte-equal
  output.
- The Sidecars pages in `webapp_v2` read listeners from the document today;
  they move to listener endpoints in step 2.
- Role names couple to sidecar names (`<sidecar>.<listener>`), so a sidecar
  rename must rename its roles.

**Unchanged**

- The sidecar binary, its config schema and the handshake wire contract.
- The gateway ↔ agent contract.

**Open questions**

- When a listener is removed, do its rule bindings **cascade** (as
  `*_rules_connections` already do in the gateway) or **restrict** (as the
  control plane does today, in code)? Recommendation: cascade, with the UI
  naming the bindings that go.
- Role status: roles have no agent, so they read `offline`. Derive it from
  `sidecars.last_seen_at`, or hide it for `managed_by='sidecar'`.
- `GET /api/resources` does not expose `managed_by`; the resource model has
  no such field yet.

**Revisit** if step 2 alone removes enough cost that step 3 is not worth its
migration.
