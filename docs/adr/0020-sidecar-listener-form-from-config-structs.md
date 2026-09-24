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

The gateway auto-deploys; sidecars upgrade on their own schedule. Every layer
decodes the config with `DisallowUnknownFields`, so one key an older sidecar
does not know refuses its whole document.

Every merged PR is its own release (`auto-release.yml`). A PR cannot know the
version that will ship its field.

## Options considered

1. **Hand-written metadata in the docs repo**, as `connections-metadata`. A
   third copy to keep in sync; the connections version already needed
   frontend fixes.
2. **A schema endpoint on the sidecar.** The plane cannot reach a sidecar;
   sidecars poll the plane.
3. **CLI-only editing.** Removes the listener editor and does nothing for
   version skew.
4. **A `since` version per field.** The author guesses the release. A low
   guess lets an old sidecar receive a key it rejects.
5. **`GET /api/sidecars/schema`.** The UI ships in the gateway binary, so a
   build-time import carries the same data without a route.

## Decision

We will generate the listener form's schema from struct tags on
`ListenerConfig` and its blocks: `label`, `help`, `placeholder`, `protocols`,
`enum`, `default`, `fields` and `ui`. `daemon.ListenerSchema` writes
`sidecar/daemon/schema.json`, and `webapp_v2` imports it at build time.

The generator refuses a field with neither `label` nor `ui:"-"`. A test fails
when `schema.json` is stale, and another sets each field on every protocol to
check that `Validate` agrees with its `protocols` tag.

The sidecar reports the config keys and protocols it accepts on every
handshake, computed by reflection over its own `Config`. The gateway stores
them, refuses a PUT or PATCH that uses anything else with a 422 that names it,
and the UI disables it. A sidecar that reported nothing is not gated, so one
that never connected can still be authored.

## Consequences

A new listener field reaches the form with a tag in the same PR. An untagged
field fails the build.

Rule sections and top-level settings are not in the form: rule sections
belong to the rule pages (ADR-0017), and top-level settings have no UI.
Cross-field checks stay hand-written in the UI.

The gate counts keys with content. A new field without `omitempty` still
serializes as a zero value into every served document, and an older sidecar
refuses it. Stripping unknown zero values at serve time is a follow-up.

The JSONB `Scan` stays strict, so a gateway rollback still fails on rows a
newer build wrote.
