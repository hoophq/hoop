# ADR-0020: The daemon's config structs drive the sidecar listener form and its validation

- **Status:** Proposed
- **Date:** 2026-09-24
- **Author:** @luanlorenzo
- **Deciders:** @luanlorenzo
- **Linear:** EVL-315
- **Supersedes / Superseded by:** —

## Context

The control plane's listener form copied `daemon.ListenerConfig` by hand. A
field added to the daemon did not reach the UI until someone edited the JS,
and a block the form rebuilt dropped the keys it did not know:
`http.sensitive_query_params` was deleted on every save.

The gateway checked the keys of a saved config, not its values. A form could
save a value every sidecar version refuses, such as `10.30.10:1234` as a
forward destination. The sidecar then refuses the whole document and fails to
start on its next restart.

## Options considered

For the schema:

1. **Hand-written metadata in the docs repo**, as `connections-metadata`. A
   third copy to keep in sync.
2. **A schema endpoint on the sidecar.** The plane cannot reach a sidecar;
   sidecars poll the plane.
3. **CLI-only editing.** Removes the listener editor.
4. **`GET /api/sidecars/schema`.** The UI ships in the gateway binary, so a
   build-time import carries the same data without a route.

For the values:

5. **Copy the daemon's checks into the UI.** A second copy that drifts, and
   API callers skip it.
6. **A separate validation endpoint.** Not needed to block a save: the save
   itself can refuse.

## Decision

We will generate the listener form's schema from struct tags on
`ListenerConfig` and its blocks. `daemon.ListenerSchema` writes
`sidecar/daemon/schema.json`, and `webapp_v2` imports it at build time.

The Go type picks the input; the tags add what a type cannot say: `label`,
`help`, `placeholder`, `protocols`, `enum`, `default`, `fields` and `ui`.
`enum:"@name"` points at the list the validation uses. `ui:"open"` marks
suggestions that also accept free text, such as `extensions.<name>` for the
SSH identity fields.

The generator refuses a field with neither `label` nor `ui:"-"`. A test fails
when `schema.json` is stale, and another sets each field on every protocol to
check that `Validate` agrees with its `protocols` tag.

POST, PUT and PATCH on a sidecar run the daemon's own validation through
`daemon.CheckConfigBytes`. A refusal answers 422 with `problems`: one entry per
problem, prefixed with its listener. The check skips only what the sidecar's
host answers: the files a config names, and whether it has a listener yet.

The import (`PUT /api/sidecars/configuration`) is not checked: it is the
sidecar's own file, which the sidecar already validated. A `load_from_disk`
document is not checked either: the plane does not serve it.

## Consequences

A new listener field reaches the form with a tag in the same PR. An untagged
field fails `go test ./daemon`, and so CI.

Rule sections and top-level settings are not in the form: rule sections
belong to the rule pages (ADR-0017), and top-level settings have no UI.

The UI keeps a few inline checks for faster feedback; the gateway's check is
the authority. A refused save lists the problems on the page, and names
another listener when that one is the invalid one.

A stored document that is already invalid refuses every later save of its
sidecar until it is fixed. The sidecar refuses that document anyway.

What only the sidecar's host answers still fails there: a missing key file,
or a listen address the host cannot bind. The fleet view shows such a sidecar
as connected, because it does not read `last_outcome` yet.

Version skew is not handled, as before. A sidecar older than a field it
receives refuses the whole document, and a generated form offers new fields
sooner than the hand-written one did. Two ways to close it: the sidecar
reports the keys it accepts on the handshake, or each release publishes its
schema and the plane maps `reported_version` to it.
