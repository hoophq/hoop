# controlplane

> **Status: foundation.** No implementation yet. This folder currently holds
> the rules and the wire contract so the component workstreams can start in
> parallel.

Manages a fleet of [`hoopinspect`](../hoopinspect/) sidecars: config decided
once in one place and distributed everywhere, sidecars registered and
identified.

This plus the sidecar is hoop 2.0, and together they replace the gateway.
`gateway/`, `agent/` and `client/` are maintenance and bug fix only.

## How it fits together

```
                        Admin UI (frontend/)
                                  |
                 read state       |      write intent
             +--------------------+--------------------+
             |                    |                    |
         inventory           desired state         admin auth
        what is running     what should run       who may edit
             ^                    |
             |                    v
             +---- the wire: one WebSocket per sidecar ----+
                                  ^                      sidecar auth
                                  |                    who may connect
          sidecars dial out, nothing dials in
             +--------------------+--------------------+
             |                    |                    |
         sidecar              sidecar              sidecar
```

## Start here

- **`CLAUDE.md`** governs the backend: five non-negotiables, what is decided,
  what is open, the wire contract, and the scope of each component. Read the
  wire contract even if you are not building it, because everything crosses
  it.
- **`frontend/CLAUDE.md`** governs the admin UI. Nothing is built and the
  stack is not chosen.

Two files by design. They get split per component once there is code to split
and the split earns itself.

## MVP

Desired state (CRUD plus a generation number), inventory and health, admin
signup and signin, and sidecar auth. Sidecar auth carries the most open
questions, so it decides the trust anchor before it writes code.

Out of the MVP on purpose: audit and telemetry ingestion, approvals, staged
config rollout, and anything that starts a sidecar. Each is noted in
`CLAUDE.md` where it would otherwise get invented by accident.
