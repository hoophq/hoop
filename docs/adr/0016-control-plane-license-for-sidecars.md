# ADR-0016: The control plane licenses the sidecar fleet

- **Status:** Proposed
- **Date:** 2026-09-11
- **Author:** @rogerio
- **Deciders:** @rogerio, @chico, @felipe
- **Supersedes / Superseded by:** —

## Context

A sidecar resolves its license from three local sources: the `-license` flag,
`HOOP_LICENSE`, and the config file's `license` key. Licensing a fleet
therefore means editing every machine in it.

The control plane already holds the organization's license in
`private.orgs.license_data`, verified when an admin posts it to
`PUT /orgs/license`. It sends nothing of it to any sidecar. A sidecar
connected to a control plane (ADR-0013) fetches its whole config from
`POST /api/sidecars/handshake` and re-runs that handshake every minute
(ADR-0014).

Three constraints bound the answer:

- The sidecar decodes the handshake body with `DisallowUnknownFields`. A key
  it does not know is a startup failure, not an ignored field.
- The license verifier exists twice on purpose (`common/license` and
  `sidecar/license`). Whatever crosses the wire is a signed document the
  sidecar checks itself; `Config.UseLicense` takes a reference and never a
  verdict, so no sender is trusted.
- One organization has one license. Sidecars are many.

## Options considered

1. **A new envelope on the handshake response** — `{"configuration": …,
   "license": …}`. The cleanest separation: a license is never config. It
   changes the response format, so every sidecar build that predates it
   refuses the body outright, and the compatibility path is a version gate on
   a response that has no versioning today.
2. **A dedicated route, polled beside the handshake** — `GET
   /api/sidecars/license`. No format change, and an old sidecar simply never
   calls it. It doubles the requests per sidecar per minute, and splits one
   fact the sidecar needs at one instant across two responses that can
   disagree.
3. **The document's existing `license` key, filled at serve time** — chosen.

An earlier position in the same discussion had the first license to reach the
control plane win, so a licensed standalone sidecar could seed an unlicensed
plane. It lost to "the plane is always the source of truth", which removes the
ordering question instead of answering it.

## Decision

We will serve the organization's license inside the configuration document the
sidecar already fetches, in the `license` key the schema already declares,
written at serve time and never stored per sidecar.

- The license stays in `private.orgs.license_data`. It is written into the
  response by `withOrgLicense` on the sidecar-authenticated routes only —
  never into the admin-facing responses the UI renders.
- Authoring a `license` key on a sidecar configuration is refused with 422.
  The import route drops it instead, because a standalone sidecar's file
  legitimately carries one and the connect journey must not fail over it.
- A configured control plane is the ONLY license source. The three local
  sources (the flag, `HOOP_LICENSE`, the config file's key) are ignored in
  plane mode, out loud: a plane whose organization holds no license runs the
  free tier, and startup warns naming the local source it did not use. They
  rank exactly as before for a standalone sidecar.
  Local sources as a fallback was the first cut and is worse: a license
  REMOVED from the plane drops a running process to the free tier, so a
  fallback would have a restart relicense it, and an admin could never
  unlicense a fleet whose pods carry their own.
- A license replaced in the control plane is adopted on the next heartbeat,
  through `Config.UseLicense`, without a restart. A document that fails
  verification is logged and dropped; the license in use stays, because a
  license nobody can read must not also freeze the rules.
- The control plane is not required to hold a license for a sidecar to
  connect. Without one the sidecar runs the free tier, and the first-access
  screen in the control plane UI is what asks an admin for one.

## Consequences

Licensing a fleet becomes one paste in one screen, and renewing it reaches
every sidecar within a minute. No sidecar row ever holds a license, so there
is one place to renew and no copy to go stale.

The cost is that a license now travels inside a config document, which reads
oddly: the two are different kinds of fact with different owners. We pay it
because the alternative is a wire format change to a response contract that
has no version negotiation. Revisit this if the handshake ever grows a
versioned envelope for another reason — the license should move out with it.

A narrower license arriving at a running relay is not applied: `buildLanes`
refuses the reload and the running rules keep serving under the license they
were built with until a restart. Dropping a guardrail or a mask rule from a
live proxy leaks more than it saves, which is the same reason `watchLicense`
stops a process instead of re-applying caps. A control plane that renews a
license into a smaller one, rather than a larger one, needs that gap closed.

A control plane that becomes unreachable leaves the sidecar on the last
license it received, for as long as the process runs. This is deliberate —
killing a data-path proxy over a lost connection is an outage — and it means
the control plane cannot revoke a license from a partitioned sidecar. A
grace period would close that and has not been specified.

Connecting a licensed standalone sidecar to a control plane that holds no
license takes its caps away. That is the decision working as intended, and it
is an upgrade path an operator can walk into: the warning at startup names the
license it stopped using, and the fix is to set the organization's license in
the control plane.
