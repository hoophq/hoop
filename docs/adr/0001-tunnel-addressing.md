# ADR-0001: Virtual network addressing scheme for `hsh tunnel`

- **Status:** Accepted (amended 2026-05-29 — dual-stack; see "Amendment" below)
- **Date:** 2026-05-18
- **Linear:** RD-204
- **Related:** RD-176 (transport spike), RD-183 (`hsh tunnel` CLI), RD-182 (connection list API), RD-208 (DNS routing), RD-217 (macOS support)

> **Amendment (2026-05-29):** the original "IPv6-only" decision below was
> superseded by a **dual-stack** scheme (ULA IPv6 **and** CGNAT IPv4)
> after macOS support landed. The body of the ADR is preserved for the
> historical rationale; read the **"Amendment: dual-stack"** section at
> the end for what actually ships and why.

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

## Amendment: dual-stack (ULA IPv6 + CGNAT IPv4)

- **Date:** 2026-05-29
- **Linear:** RD-217 (macOS support), RD-208 (DNS routing)

### What changed

The virtual network is now **dual-stack**. Every connection name is
allocated **both**:

- a ULA IPv6 address inside the per-session `fd00::/8` `/48` (unchanged), and
- a CGNAT IPv4 address inside a per-session `100.64.0.0/10` `/16`.

The resolver answers `AAAA` with the v6 address **and** `A` with the v4
address. The netstack registers both IPv4 and IPv6 in gVisor, owns a
gateway address per family, routes both ranges locally, and accepts TCP
flows over either. Outbound reverse lookup (IP → name) accepts both
families, so the gateway still never sees virtual IPs.

### Why the original "IPv6-only" decision was wrong in practice

It broke macOS, which is a first-class client (RD-217). macOS's
`getaddrinfo()` honours `AI_ADDRCONFIG`: it will not even **query** — let
alone return — `AAAA` records for a hostname unless the host has a
*globally-routable* IPv6 address. A machine whose only IPv6 is the tunnel
itself (the common case: no v6 from Wi-Fi/Ethernet) does not qualify, so
every real application (`psql`, `curl`, browsers — anything using
`getaddrinfo` with default hints) silently fails to resolve `*.hoop`,
while `dig`/`ping6` (which bypass `AI_ADDRCONFIG`) work and mislead you
into thinking DNS is fine.

The documented workarounds to make macOS treat a `utun` v6 address as
routable — registering a `SystemConfiguration` network service, adding
broad v6 routes — **do not** flip macOS 26's resolver out of "Request A
records" mode (verified empirically). Fighting `AI_ADDRCONFIG` is a
losing, version-fragile battle.

IPv4 has no equivalent gating: macOS always queries `A` and always uses
the answer. Handing out an A record sidesteps the entire problem on every
platform with no per-OS branching in the data path.

### Revisiting the original CGNAT rejection

The 2026-05-18 ADR rejected `100.64.0.0/10` for two reasons; both are
acceptable now that v4 is **additive**, not the sole family:

- *"Collides with real ISP CGNAT / Tailscale."* The collision risk is
  real but bounded: the range is non-globally-routable by definition, we
  scope to a per-session `/16` (not the whole `/10`), and a user who is
  simultaneously behind ISP CGNAT *and* running Tailscale *and* hitting
  our exact `/16` still has the IPv6 address as the parallel path. CGNAT
  remains the least-bad v4 choice — RFC 1918 `10/8` and `192.168/16`
  collide with corporate/home LANs far more often, which is strictly
  worse.
- *"Sizing math past a few thousand resources."* A `/16` gives ~65k
  addresses per session, far beyond any realistic per-user connection
  count, with linear probing for the rare hash collision.

The "future-proof for IPv6-only networks" argument still holds — and is
preserved: we did not drop IPv6, we added IPv4 alongside it. On a genuine
IPv6-only host, `AI_ADDRCONFIG` is satisfied and the AAAA path works; on
the far more common IPv4-capable host, the A path works. Dual-stack is
strictly more robust than either alone.

### Updated resolver behaviour

Supersedes the "Resolution: client-owned static table" section:

- `<name>.hoop` AAAA → mapped ULA IPv6.
- `<name>.hoop` A → mapped CGNAT IPv4 (was: NOERROR empty).
- Negative answers (NXDOMAIN for unknown names; NODATA for an
  unsupported qtype on a known name) carry an SOA in the authority
  section per RFC 2308, which stub resolvers (notably macOS) require to
  accept and cache the negative answer.

### Known limitation

`ping` works over IPv6 but not IPv4: gVisor's promiscuous-mode IPv6 stack
answers ICMPv6 echo for on-demand addresses, but its IPv4 stack declines
ICMPv4 echo for "temporary" (spoofed) addresses. The tunnel is TCP-only,
so this is cosmetic — test with `nc`/`psql`, not `ping`. A custom ICMPv4
echo responder was judged not worth the complexity.
