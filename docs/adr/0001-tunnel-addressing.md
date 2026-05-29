# ADR-0001: Virtual network addressing scheme for `hsh tunnel`

- **Status:** Accepted
- **Date:** 2026-05-18
- **Linear:** RD-204
- **Related:** RD-176 (transport spike), RD-183 (`hsh tunnel` CLI), RD-182 (connection list API), RD-208 (DNS routing)

## Context

`hsh tunnel` opens a long-lived session against the gateway and exposes every connection the user is authorized to reach as a hostname on the user's machine. The UX target is "feels like a corporate VPN": users address `pg-prod.hoop` and get a working TCP socket on port 5432.

Several questions had to be settled before any implementation could start:

1. What top-level domain do tunneled hosts live under?
2. What IP family and range backs the virtual network?
3. Who allocates virtual IPs, and are they stable?
4. How is name resolution wired into the OS?

This ADR captures the decisions. Alternatives considered are at the bottom.

## Decision

### TLD: `.hoop`

All tunneled resources live under the `.hoop` pseudo-TLD. Example: `pg-prod.hoop`, `bastion.hoop`, `redis-cache.hoop`.

`.hoop` is not in the IANA root zone and is not on the [reserved special-use TLD list](https://www.iana.org/assignments/special-use-domain-names/special-use-domain-names.xhtml), so collision risk is low but not zero. An org running an internal `.hoop` zone (rare) can override the suffix:

```bash
export HSH_TUNNEL_DOMAIN=hoop.internal
```

The client validates that the override is a single label or dotted label sequence; subdomains under the override (e.g. `pg-prod.hoop.internal`) work the same way.

### IP range: ULA IPv6 (`fd00::/8`, RFC 4193)

The virtual network is **IPv6-only** using Unique Local Addresses. The client picks a `/48` from `fd00::/8` deterministically per tunnel session (hash of org ID + tunnel session ID) and allocates resource addresses inside it.

Rationale:

- **No collision with corporate IPv4.** Customers running large RFC1918 networks (10.0.0.0/8, 172.16.0.0/12) can't conflict with us.
- **Effectively unbounded address space.** No subnet sizing math, no "run out of IPs at 65k resources" failure mode.
- **Future-proof for IPv6-only networks.** A growing fraction of cloud and corporate networks are dropping IPv4; if we had picked CGNAT v4 we'd block those customers.
- **TUN setup is simpler with a single family.** Configuring both v4 and v6 on the userspace netstack adds complexity for no UX benefit.

The downside — applications that hard-code IPv4 — is acceptable for this product. Database clients, HTTP clients, SSH, kubectl, etc. all handle v6 transparently. Anything that breaks here is broken against any v6-only environment.

### Allocation: deterministic name → IP

Each connection name maps to a virtual IP via a stable hash function (e.g. SHA-256 of `name`, take the low 80 bits, prepend the session `/48`). Properties:

- The same connection name gets the same IP across tunnel restarts within a session lifetime.
- IPs are immutable for the lifetime of a tunnel session. Refreshing the connection list (RD-182) may **add** new mappings but must never **reassign** an existing name's IP.
- Different clients connecting to the same gateway can pick different IPs for the same resource. That's fine — only the **name** is meaningful on the wire (see "Gateway never sees virtual IPs" below).

Determinism matters for caches: psql connection-cache files, password managers keyed by hostname, scripts that pin a host all keep working across restarts.

### Stability: addresses are immutable for the tunnel session lifetime

In-flight TCP connections must not break when the connection list refreshes. The static-table resolver only adds entries; it never mutates. If a connection is revoked the entry is removed, but the IP is not recycled within the same session — a stale TCP attempt to a revoked resource gets a clean failure from the gateway, not a wrong-resource hijack.

### Resolution: client-owned static table

The DNS resolver inside the tunnel owns a static map of `name → virtual-IP` populated from the connection list (RD-182). It is **not** recursive. Behaviour:

- `<name>.hoop` AAAA → mapped virtual IPv6
- `<name>.hoop` A → NOERROR with empty answer (we are v6-only)
- Unknown name under `.hoop` → NXDOMAIN immediately

NXDOMAIN-on-typo is a major UX win over the alternative (TCP-level "connection refused after 30s timeout").

When the TUN packet handler receives an outbound packet, it does the reverse lookup (IP → name) using the same table and frames the WebSocket message with the connection name. **The gateway never sees virtual IPs.** This decouples the gateway from address allocation entirely.

### DNS resolver location: bound to the tunnel gateway IP

The resolver listens on the userspace netstack's gateway address, e.g. `[fd00:hash:hash:hash::1]:53`. Pros:

- No loopback binding, no port-53 privilege requirement on the host.
- The OS-level DNS routing (RD-208) just points `*.hoop` queries at this address; everything else flows through the user's normal DNS.
- The resolver lives entirely inside the tunnel process — it goes away cleanly when the tunnel closes.

## Consequences

### Positive

- **No port collisions.** Multiple Postgres connections each get their own v6 address; all listen on 5432.
- **No IP allocation server on the gateway.** Client picks; gateway routes by name.
- **Survives reconnects.** Same name → same IP after a transient WS drop.
- **Fast NXDOMAIN.** Typo'd hostnames fail in milliseconds, not seconds.

### Negative / open risks

- **IPv6-only requirement.** Apps that ship with hard-coded `AF_INET` socket calls will not work. We accept this; in practice the affected set is tiny.
- **`.hoop` is not an IANA-reserved suffix.** If ICANN ever delegates it, we have a problem. Mitigation: `HSH_TUNNEL_DOMAIN` escape hatch is already in scope.
- **No multi-tenant IP allocation.** Two tunnels from the same user in different sessions can pick different IPs for the same name. Not a problem in practice (one tunnel at a time per user) but worth noting if we ever support overlapping tunnels.

## Alternatives considered

### IPv4 CGNAT range (`100.64.0.0/10`)

Rejected: collides with real ISP CGNAT deployments and with Tailscale, which sets a hard precedent for confusion. Sizing math also gets painful past a few thousand resources.

### Private IPv4 (`10.x.x.x` / `172.16.x.x`)

Rejected: guaranteed collision with corporate networks.

### Port-mapped loopback (`127.0.0.1:<random-port>`)

Rejected: doesn't match the UX target. Users would type `localhost:54321` instead of `pg-prod.hoop`. Also breaks any tooling that expects a hostname (psql `~/.pgpass`, k8s kubeconfig, etc.).

### Hostname-only resolution (no virtual IPs)

Considered briefly: resolver returns a sentinel IP, TUN driver figures out the name from the SNI / TCP stream. Rejected because it doesn't work for raw TCP protocols (Postgres, MySQL, Redis, plain SSH) where there's no early signal carrying the hostname.

### Gateway-allocated IPs

Rejected: adds a coordination service to the gateway for no benefit. The gateway doesn't need to know about virtual IPs because resolution happens client-side.
