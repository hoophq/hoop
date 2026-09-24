# ADR-0019: A sidecar's config file rules become control-plane rule items

- **Status:** Proposed
- **Date:** 2026-09-24
- **Author:** @rogerio
- **Deciders:** @rogerio
- **Supersedes / Superseded by:** amends ADR-0017

## Context

A sidecar that joins the control plane pushes its config file once
(`PUT /sidecars/configuration`). Before this change the plane stored the file
as one document, rules included. The Guardrails, Data Masking and AI Session
Analyzer pages did not show those rules, so an admin could not see or edit
what the sidecar enforced. ADR-0017 composes bound rule items into the served
document on every handshake. Rules left inside the stored document sit
outside that path.

An admin can also switch a sidecar back to its own file (`load_from_disk`),
and then back to the plane. Each switch must say what happens to the rules.

Three facts bound the answer:

- **Order is policy.** The sidecar evaluates rules in order and the first
  match wins. A lane's own guardrails run before the top-level ones. Mask
  entries apply as listed.
- **Rules live in shared, org-wide tables.** An imported rule can be bound
  later to other sidecars or connections. An admin writes rules in the same
  tables.
- **`managed_by` already exists and means "read-only".** The API and the UI
  refuse to edit a rule that carries it.

## Options considered

1. **Keep the file rules in the stored document.** No new rows. The rules
   stay invisible on the feature pages, and editing one means editing JSON.
   This is what the feature exists to end.
2. **Split the file into rule items, and mark provenance with `managed_by`.**
   Reuses a column. It makes every imported rule read-only, so the admin
   cannot take over what they imported.
3. **Split the file into rule items, with an ordered binding and a separate
   provenance column** (chosen).

For the switch to the file, three deletion rules were weighed: delete every
rule bound only to this sidecar (it also deletes rules an admin wrote), never
delete (it leaves orphan rules after each round trip), or delete only what
this sidecar's file brought.

## Decision

**We split each imported file into rule items, keep the file's order in the
binding, and record which sidecar brought each rule.**

- Each guardrail and mask entry becomes one rule item. Each listener analyzer
  block becomes one analyzer rule. Each item is bound to the listeners that
  ran it. The stored document keeps everything except the rules.
- The three junction tables gain `position`. A guardrail keeps the daemon's
  order: lane rules first, then top-level rules. A mask entry keeps its list
  index. A re-save keeps the stored position; a new binding goes last.
  Composition orders by `listener_name, position, name`.
- The three rule tables gain `imported_from_sidecar` (nullable UUID). NULL
  means an admin wrote the rule.
- **To the file:** the plane deletes only rules with `imported_from_sidecar`
  equal to this sidecar, no rulepack, and no target left. Every other rule
  is unbound. `PATCH` returns the lists in `detached_rules`.
- **Back to the plane:** the stored document is emptied. The sidecar pushes
  its file again on its next heartbeat (the handshake answers 412).
- **Only `PATCH` switches.** `PUT` refuses a `load_from_disk` change, and
  `load_from_disk: false` must be sent alone, because the switch deletes rows
  and resets the document.
- **The import refuses what the rule pages refuse.** Each spec goes through
  `ValidateSidecarRuleSpec` (422). Names are picked inside the import
  transaction; a name taken meanwhile answers 409.

### Amending ADR-0017

ADR-0017 says the stored document stays what an admin authored. For an
imported sidecar it is no longer the file as pushed: the import removes the
rules and writes them as items. The served document is still the file's,
because composition folds the items back in the same order.

## Consequences

**Good.**

- What a sidecar enforces is visible and editable on the feature pages.
- The served config after an import enforces what the file did, in the same
  order. A test composes an import through the database and checks the order.
- A rule an admin wrote survives any number of switches.

**Costs, accepted.**

- The sidecar token now writes org-wide rows: rule items and access request
  rules for held analyzer levels.
- Sidecars imported before this change keep their rules in the stored
  document. Nothing migrates them.
- Every new feature that binds rules to listeners must write `position`, or
  its rules run in name order.

**Revisit if** a sidecar that predates the heartbeat re-import ships to
customers. The switch back would then leave it with an empty document until
it restarts.
