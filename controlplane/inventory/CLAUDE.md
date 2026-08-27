# CLAUDE.md: inventory

What is **actually** running: which sidecars exist, what version, which config
generation each has applied, when each last checked in.

Read `../CLAUDE.md` and `../transport/CLAUDE.md` first.

`desiredstate/` holds what should be true. This package holds what is true.
The two disagreeing is the normal condition, not an error, and showing the
difference honestly is the product.

## Owns

- The live fleet view: one record per connected sidecar.
- Liveness, from the socket and the `status` heartbeat.
- The applied generation, taken from `config.ack`.
- Staleness, computed.

## Storage is memory

Deliberate, and it has one consequence worth writing down: **after a control
plane restart the fleet view is empty until sidecars reconnect.** Sidecars
reconnect on their own and send `hello` with their current generation, so the
view rebuilds itself within roughly one reconnect backoff. State that window
in the UI rather than showing an empty list that looks like an outage.

The other consequence: memory-only rules out running two control plane
replicas, because each would hold a different view of the fleet. Fine for
self-hosted single instance, which is the MVP target. Revisit before HA.

## Liveness is the socket, not a poller

With a persistent WebSocket, "connected" is "the socket is open". There is no
health-check loop and nothing here dials a sidecar (rule 2).

The heartbeat is still required. TCP holds a half-open connection for a long
time, so a sidecar that vanished still looks connected until a `status` fails
to arrive within its interval.

## Never report a generation you were not told

The applied generation comes from `config.ack` and from `hello`. Nowhere else.

Do not infer it from "we sent generation N, so it must be running N". That
assumption is exactly what `config.nack` exists to break, and inferring it
means the fleet view confidently reports a rule as enforced when the sidecar
refused it.

## States

| State | Means |
|---|---|
| `connected` | socket open, heartbeat current, acked generation equals issued |
| `stale` | socket open, but the acked generation is behind the issued one past the heartbeat window |
| `rejected` | the sidecar sent `config.nack`; carry the reason, it is the only thing the operator can act on |
| `disconnected` | socket closed, record retained with a last-seen timestamp |

## MVP scope

Done means: an admin can list every connected sidecar with its version,
applied generation, state and last check-in, and a sidecar that reconnects
after a control plane restart reappears without manual action.

## Gotchas

- **Retain disconnected records, briefly.** A sidecar that drops off the list
  the instant its socket closes makes a restart look like the sidecar was
  deleted. Keep the record with a last-seen time and let it age out.

- **NACK reasons are the highest-value field here and the easiest to drop.**
  It is the one signal that tells an operator a config is broken and why. It
  should surface in the fleet view, not only in a log line.

- **Version skew shows up here first.** A run of `unsupported` or `config.nack`
  from older sidecars after a control plane deploy is the standard early
  warning that a change broke the contract. Make that countable.
