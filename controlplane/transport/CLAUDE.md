# CLAUDE.md: transport

The WebSocket channel between a sidecar and the control plane, and the message
contract every other component speaks.

Read `../CLAUDE.md` first. The five non-negotiables there are mostly enforced
here.

**This contract is a proposal until signed off.** It is written down so the
other four components can be built in parallel against a fixed shape instead
of each inventing one. Change it by changing this file first, then the code.

## Shape

One WebSocket per sidecar. The sidecar dials, the control plane accepts.
Nothing in the control plane ever dials a sidecar (rule 2).

Every frame is one JSON object:

```json
{ "v": 1, "type": "config.apply", "id": "01J...", "payload": { } }
```

| Field | Meaning |
|---|---|
| `v` | envelope version, currently `1` |
| `type` | message type, from the table below |
| `id` | ULID, unique per message; a reply carries `re` set to the id it answers |
| `re` | optional, the `id` this message replies to |
| `payload` | type-specific, may be absent |

## Message types

Up means sidecar to control plane. Down means control plane to sidecar.

| Type | Dir | Payload | Notes |
|---|---|---|---|
| `hello` | up | sidecar id, build version, listener summary, applied generation | First frame after connect. Always. |
| `hello.ok` | down | assigned sidecar id, heartbeat interval | Reply to `hello`. Nothing else flows until this is sent. |
| `hello.reject` | down | reason | Auth failed or version unsupported. Control plane closes after sending. |
| `config.apply` | down | full `sidecar.Config` document, generation | Not a delta. See below. |
| `config.ack` | up | generation | The sidecar is now running this generation. |
| `config.nack` | up | generation, reason | The sidecar could not apply it and is still running the previous one. |
| `status` | up | applied generation, uptime, per-listener health | Heartbeat. Sent on the interval from `hello.ok`. |
| `unsupported` | both | the `type` that was not understood | Never drop an unknown type silently. |

Reserved, not in the MVP, listed so nobody reuses the names: `approval.request`
(up), `approval.result` (down), `audit.batch` (up).

## Rules

**Full documents, not deltas.** One sidecar's config is small. Delta encoding
earns its complexity at Envoy's endpoint-churn scale, where a 1,000-pod
cluster re-sends 1,000 unchanged records to add one. We are nowhere near that,
and a full document makes the ACK unambiguous: the sidecar is running
generation N or it is not. Revisit if a real config passes a few hundred KB.

**NACK is mandatory, not optional politeness.** A sidecar that cannot apply a
config must say so. Silence means `inventory/` reports a generation the
sidecar is not running, which is worse than reporting a failure, because the
operator believes a rule is enforced when it is not.

**Unknown fields are ignored. Unknown types get `unsupported`.** This is how
rule 5 is enforced in practice. An old sidecar receiving a message from a
newer control plane replies `unsupported` and keeps running. It does not close
the socket and it does not fail.

**`hello` gates everything.** Config does not flow before `hello.ok`. This is
also where `sidecarauth/` hooks in: the credential is presented at connect
time, and a failed check produces `hello.reject`, not a silent drop.

**The generation belongs to `desiredstate/`.** Transport carries it and never
invents it. Monotonic per sidecar.

**Reconnect is catch-up, not replay.** A sidecar that reconnects sends `hello`
with the generation it is running. If that is behind, the control plane sends
one `config.apply` with the current document. There is no queue of missed
messages to drain.

## Gotchas

- **Go has no WebSocket in the standard library.** On the control plane side a
  dependency is fine. On the **sidecar** side it is not (rule 4), so the client
  belongs in a nested module under `hoopinspect/`, never in the root. If that
  proves awkward, the fallback that keeps the invariant is SSE down plus POST
  up: both are pure `net/http`, at the cost of two connections instead of one.

- **A heartbeat is not redundant with an open socket.** TCP will hold a
  half-open connection for a long time. Without `status` on an interval, a
  sidecar that has silently gone away still looks connected.

- **Do not put an approval on the config path.** Approvals are synchronous and
  a human is waiting. Config distribution is eventually consistent. They share
  the socket and nothing else. The reserved `approval.*` types exist so this
  stays true when the feature lands.
