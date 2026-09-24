# ADR-0016: The control plane licenses the sidecar fleet

> **Amendment (2026-09-14):** review of the implementation found three things
> this record got wrong, none of which changes the decision. A control plane
> only owns the licensing decision when it SAYS so, through a response header,
> because an older gateway's silence is not an answer. A license that grants
> less than the running rules need now stops the relay instead of being
> ignored. And the exposure of the signed document to any sidecar token holder
> is recorded below as a consequence we accepted rather than one we missed.

- **Status:** Accepted (amended 2026-09-14; implemented in PR #1812)
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
- A control plane that MANAGES licensing is the only license source. The three
  local sources (the flag, `HOOP_LICENSE`, the config file's key) are ignored
  under one, out loud: such a plane whose organization holds no license runs
  the free tier, and startup warns naming the local source it did not use.
  They rank exactly as before for a standalone sidecar.
  A plane says it manages licensing with the `hoop-sidecar-license-managed`
  response header. A gateway older than this feature sends nothing, and a
  sidecar cannot tell that silence from "the organization has none" by the
  document alone, because the key is `omitempty`. Without the header an
  upgraded sidecar would discard the operator's own license, and a config that
  needs the paid caps would not start at all -- a sidecar can be upgraded
  before the gateway it talks to. The signal is a header for the same reason
  the license is not a new field: the body is decoded with
  `DisallowUnknownFields`.
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

A license that grants LESS than the running rules need stops the relay. The
reload cannot apply it -- dropping a guardrail or a mask rule from a live
proxy leaks more than it saves -- so the process publishes the new license,
drains, and exits; the supervisor restarts it and `buildLanes` refuses the
config by name until somebody renews the license or removes rules. This is
the same controlled stop an ended term gets, and it is why the reload path
cannot simply keep the old license: the heartbeat records a refused document
as handled, so nothing would revisit it and the organization would serve paid
rules until the term ended.

A control plane that becomes unreachable leaves the sidecar on the last
license it received, for as long as the process runs. This is deliberate —
killing a data-path proxy over a lost connection is an outage — and it means
the control plane cannot revoke a license from a partitioned sidecar. A
grace period would close that and has not been specified.

The signed document is served whole to anyone holding a sidecar token, and the
sidecar verifier does not enforce its `allowed_hosts` -- deliberately, since a
sidecar's only hostname is a scheduler-generated pod name. A token holder can
therefore lift the document and run it on an unrelated standalone sidecar.
What leaks is entitlement, not customer data, and that token already fetches
the whole configuration: upstreams, guardrails, masking rules. Closing it
properly means issuing a grant bound to the organization and the sidecar,
which changes the license format that `client/licensecompat` pins across both
verifiers. That is a contract change, deferred rather than dismissed.

Connecting a licensed standalone sidecar to a control plane that holds no
license takes its caps away. That is the decision working as intended, and it
is an upgrade path an operator can walk into: the warning at startup names the
license it stopped using, and the fix is to set the organization's license in
the control plane.
