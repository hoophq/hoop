# ADR-0014: Sidecar config hot reload for rule-only drift

- **Status:** Proposed
- **Date:** 2026-09-08
- **Author:** @matheusfrancisco
- **Deciders:** TBD
- **Supersedes / Superseded by:** —

## Context

A sidecar connected to the Control Plane (DEP-197) fetches its whole config
from `POST /api/sidecars/handshake` at startup and re-runs the handshake
every minute as a heartbeat. The gateway rebuilds the document from the
database on every call, so a guardrail or masking rule edited in the UI is
in the next answer. The gateway also serves `GET /sidecars/configuration`:
the same document under the same `hoop-sidecar-token` auth, with no side
effects, so a poll never overwrites the version and last-seen the sidecar
reported at its last handshake.

Today the sidecar only logs the drift: `the control plane configuration
changed; restart to apply it`. A restart closes every live client
connection. For a fleet where rule edits are routine, each edit costs
either an outage window per sidecar or an unbounded delay until someone
restarts it. The sidecar fronts databases; its clients hold long-lived
connections (`psql`, connection pools) that reconnect on their own
schedule.

Two facts about the daemon constrain the design:

1. `proxy.Server.handle` captures `Policy` and `Masker` once per accepted
   connection, into that connection's Gate (`sidecar/proxy/proxy.go:351`).
   Nothing on the statement path reads them from shared state.
2. Listener topology (bind address, upstream, protocol, TLS posture) is
   bound at startup. Changing it means closing and opening sockets under
   live traffic.

Config drift therefore splits into two classes: rule content changed
(guardrails, masking, pii narrowing, OPA endpoints) with the listener set
identical, and topology changed (a listener added, removed, or re-pointed).

Related constraints:

- `buildLanes` enforces the license caps at startup. Any reload path that
  skips it turns the heartbeat into a cap bypass.
- The PII detector is built by the entry points (`PluginBuilder`) and handed
  to `Run` ready-made; the daemon cannot rebuild it today.
- gRPC lanes (ADR-0013) receive their evaluator and masker through
  `buildGRPCServer`'s callback injection, a different seam from relay lanes.

## Options considered

1. **Restart-only (status quo).** The heartbeat logs, an operator or a
   container restart policy applies. Simple, already shipped. Loses live
   connections on every rule edit and leaves a window where the fleet
   enforces stale rules for as long as restarts take.
2. **Full hot reload, topology included.** Rebind listeners, drain removed
   lanes, dial new upstreams in place. Handles every drift, and is the only
   option that never needs a restart. Loses to complexity: port rebinding
   races, a drain policy for removed lanes, and failure states where half
   the new topology is live. The rare event (topology change) would carry
   the risk for the common one (rule edit).
3. **Rule-only hot swap behind a topology guard.** When the listener
   identity set is unchanged, rebuild each lane's evaluator and masker from
   the fetched config and swap them atomically; existing connections keep
   the Gate they started with, new connections get the new rules. Any
   topology difference keeps today's restart log. Covers the common case
   with a small, bounded change; the capture-at-accept design in fact (1)
   makes the swap point one field.

## Decision

We will implement option 3, in stages:

1. **Poll endpoint** (shipped with this ADR's PR): `GET
   /sidecars/configuration` on the gateway, sharing `respondConfig` with the
   handshake so the two answers cannot drift.
2. **Swap point:** `proxy.Server` holds `{Policy, Masker, FailOnAuditError}`
   in an `atomic.Pointer`, loaded once per accepted connection. The current
   unsynchronized `s.cfg.Policy` read becomes the pointer load; nothing else
   on the accept path changes.
3. **Reload in the heartbeat:** on drift, run the fetched config through the
   same pipeline startup uses — `LoadConfigBytes`, per-listener `resolve`,
   `buildPolicy`, masker build, and the license cap checks in `buildLanes`.
   A config the caps refuse is logged and not applied, the same verdict
   startup would reach. Lanes match by listener name; the topology guard
   compares `name`, `protocol`, `network`, `listen`, `upstream`, and both
   TLS blocks. Any mismatch, addition, or removal falls back to the restart
   log.
4. **Detector rebuild:** the entry points pass their `PluginBuilder` into
   setup as a new `Option`, so a changed `pii` section rebuilds the detector
   the way startup would. Without the option, a `pii` or `mask` drift falls
   back to the restart log rather than swapping rules the old detector
   cannot serve.
5. **gRPC lanes stay on the restart path** in this iteration, with a log
   line saying so. Their swap needs its own seam through the ADR-0013
   callback injection and earns its own change.

Swap semantics are connection-granular: a connection opened before the swap
keeps the rules it started with until it closes; every connection accepted
after the swap runs the new rules. Statement-granular semantics (a live
session picks up new rules on its next statement) move the atomic load into
the Gate's per-statement path and are deferred until connection-granular
proves itself in the field.

## Consequences

Easier: a rule edited in the UI reaches every connected sidecar within one
heartbeat, with zero dropped connections. Fleet-wide policy changes stop
being restart campaigns.

Harder: one process can run two rule generations at once, old Gates beside
new ones. The admin `/config` and `/stats` endpoints must say which config
generation is active, or an operator debugging "why did this not deny"
reads rules a draining connection no longer runs. Session audit events need
nothing: each Gate already records what it enforced.

Committed: the reload pipeline and the startup pipeline stay one code path.
A config startup would refuse, reload refuses for the same reason with the
same message. The moment those diverge, the heartbeat becomes a side door
past validation and the caps.

Revisit if topology drift turns out to be common (connections re-pointed at
new hosts as a matter of routine). That is option 2's territory: listener
rebinding with a drain policy, and this ADR's guard becomes the fallback
rather than the boundary. Revisit the connection-granular choice if
customers expect an edited deny rule to bind existing sessions; that is the
statement-granular follow-up, not a reason to hold this stage.
