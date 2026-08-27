# CLAUDE.md: desiredstate

What config each sidecar **should** be running, and the generation number that
identifies it. Simple CRUD plus one invariant.

Read `../CLAUDE.md` and `../transport/CLAUDE.md` first.

## Owns

- The config entities an admin creates and edits, stored in the database.
- The mapping from a sidecar to the config document it should run.
- The **generation**: a monotonic integer, per sidecar, bumped whenever that
  sidecar's effective config changes.

## Does not own

- Delivery. `transport/` carries the document. This package decides what the
  document is.
- What is actually running. That is `inventory/`, and the two disagreeing is
  normal, expected, and the whole reason both exist.

## The output is `hoopinspect/sidecar.Config`

Not a new schema. That type already exists, is JSON-native, and validates
exhaustively on load, reporting every problem rather than failing on the
first.

Two consequences:

**Validate before you store, using the same validation the sidecar runs.** A
config that the UI accepted and the sidecar rejects is the worst outcome
available: the admin believes a rule is live, `inventory/` shows a NACK nobody
reads, and the rule is not enforced. Catching it at write time turns a fleet
incident into a form error.

**Store and ship JSON.** YAML needs the nested `hoopinspect/config/yaml`
module. Sending YAML forces every sidecar to link a parser it otherwise does
not need.

## Generation rules

- Monotonic per sidecar. Never reused, never decremented.
- Bumped on any change to a sidecar's **effective** config, including a change
  to something shared that the sidecar inherits.
- Recorded when issued. `inventory/` compares what was issued against what was
  acknowledged, so an issued generation that nobody stored is a generation
  that can never be reconciled.

## MVP scope

Done means: an admin can create, read, update and delete config entities; the
system can produce a valid `sidecar.Config` for a given sidecar; and every
change bumps that sidecar's generation.

Mapping is **per sidecar** for the MVP. Grouping by label or tag is the
obvious next step and the schema should not make it painful, but do not build
it yet.

## Gotchas

- **Do not add a second source of truth.** No file-watching path, no "import
  from YAML on every boot", no environment override that silently wins. The
  bootstrap file is read once by the sidecar at startup and never again. Two
  sources of truth is the specific failure this design already decided to
  avoid.

- **A config change touches the whole fleet at once.** There is no staged
  rollout in the MVP. Do not add anything that makes one impossible later: keep
  the "which sidecars does this apply to" question answerable in one place, so
  a future canary is a filter and not a rewrite.

- **Deleting a config entity is not the same as reverting a sidecar.** Decide
  explicitly what a sidecar runs when its config is deleted, and make the
  answer loud. A sidecar silently dropping to an empty policy is an open
  database with an audit trail that says everything was fine.
