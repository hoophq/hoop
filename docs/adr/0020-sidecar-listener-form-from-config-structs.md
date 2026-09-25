# ADR-0020: The sidecar listener form is generated from the daemon's config structs

- **Status:** Proposed
- **Date:** 2026-09-24
- **Author:** @luanlorenzo
- **Deciders:** @luanlorenzo
- **Linear:** EVL-315
- **Supersedes / Superseded by:** —

## Context

The control plane's listener form copied `daemon.ListenerConfig` by hand. A
field added to the daemon did not reach the UI until someone edited the JS.
A block the form rebuilt dropped the keys it did not know:
`http.sensitive_query_params` was deleted on every save.

## Options considered

1. **Hand-written metadata in the docs repo**, as `connections-metadata`. A
   third copy to keep in sync; the connections version already needed
   frontend fixes.
2. **A schema endpoint on the sidecar.** The plane cannot reach a sidecar;
   sidecars poll the plane.
3. **CLI-only editing.** Removes the listener editor.
4. **`GET /api/sidecars/schema`.** The UI ships in the gateway binary, so a
   build-time import carries the same data without a route.

## Decision

We will generate the listener form's schema from struct tags on
`ListenerConfig` and its blocks: `label`, `help`, `placeholder`, `protocols`,
`enum`, `default`, `fields` and `ui`. `daemon.ListenerSchema` writes
`sidecar/daemon/schema.json`, and `webapp_v2` imports it at build time.

The generator refuses a field with neither `label` nor `ui:"-"`. A test fails
when `schema.json` is stale, and another sets each field on every protocol to
check that `Validate` agrees with its `protocols` tag.

A Go type cannot say that a string takes a fixed set of values, so those
fields declare `enum`, pointing with `@name` at the list the validation uses.
`ui:"open"` marks a set of suggestions that also accepts free text, such as
`extensions.<name>` for the SSH identity fields.

Every write (POST, PUT, PATCH) runs the daemon's own validation through
`daemon.CheckConfigBytes` and answers 422 with its message. It skips only what
the sidecar's host answers: the files a config names, and whether it has a
listener yet. Before this, the gateway checked keys and not values, so a form
could save a value every sidecar version refuses. The 422 lists each problem
(`problems`), prefixed with its listener, so the form shows them in place and
names another listener when that one is the invalid one.

## Consequences

A new listener field reaches the form with a tag in the same PR. An untagged
field fails the build.

Rule sections and top-level settings are not in the form: rule sections
belong to the rule pages (ADR-0017), and top-level settings have no UI.
The UI keeps a few inline checks for faster feedback; the gateway's check is
the authority.

A stored document that is already invalid refuses every later write of its
sidecar until it is fixed. The sidecar refuses that document anyway.

Version skew is not handled, as before. The form offers every field the
control plane knows, and a sidecar older than a field it receives refuses the
whole document (`refused`), then fails to start on its next restart. A
generated form offers new fields sooner than the hand-written one did. Two
ways to close it: the sidecar reports the keys it accepts on the handshake,
or each release publishes its schema and the plane maps `reported_version` to
it.
