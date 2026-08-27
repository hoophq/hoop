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
                        Admin UI (terminal, web)
                                  |
                 read state       |      write intent
             +--------------------+--------------------+
             |                    |                    |
        inventory/          desiredstate/         adminauth/
        what is running     what should run       who may edit
             ^                    |
             |                    v
             +------- transport/ (one WebSocket per sidecar) -------+
                                  ^                         sidecarauth/
                                  |                         who may connect
          sidecars dial out, nothing dials in
             +--------------------+--------------------+
             |                    |                    |
         sidecar              sidecar              sidecar
```

## Start here

1. `CLAUDE.md` in this folder. Five non-negotiables, what is decided, what is
   open.
2. `transport/CLAUDE.md`. The message contract. Every component crosses it, so
   read it even if you are not building it.
3. The `CLAUDE.md` in whichever component you picked up.

| Component | State |
|---|---|
| `transport/` | contract written, needs sign-off |
| `desiredstate/` | ready to build, simple CRUD plus a generation number |
| `inventory/` | ready to build, in-memory fleet view |
| `adminauth/` | ready to build, first admin plus signin |
| `sidecarauth/` | most open questions, decides the trust anchor then builds |

Out of the MVP on purpose: audit and telemetry ingestion, approvals, staged
config rollout, and anything that starts a sidecar. Each is noted where it
would otherwise get invented by accident.

## Conventions

The root `CLAUDE.md` does not govern this directory, the same way
`hoopinspect/` does not. Rules for this product live here.
