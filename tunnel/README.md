# tunnel/ — Hoop Tunnel daemon (`hsh-tunneld`)

A client-side tunnel daemon that lets a developer reach every hoop
connection by its name (e.g. `psql -h pg-prod.hoop`) as if those services
lived on the local network. All the "magic" is local: there is **no new
gateway protocol or endpoint**.

The daemon is shipped together with the unprivileged `hsh` CLI from the
[hoophq/hsh](https://github.com/hoophq/hsh) repo. Day-to-day, users
install both via the platform-specific tarball from the hsh GitHub
Releases page (see RD-227); a Homebrew formula will follow once the
macOS LaunchDaemon backend lands (RD-220, depends on RD-217 macOS
support). Users never invoke `hsh-tunneld` directly except for
`hsh-tunneld install` at install time and any troubleshooting steps
that need root.

For each TCP flow accepted inside the userspace netstack, the tunnel
opens a fresh gRPC bidirectional stream to the existing hoop gateway —
the same `Transport.Connect` RPC the `hoop connect` CLI uses. The
gateway sees these flows as ordinary client sessions, so authentication,
review, audit, DLP, access control, webhooks, and slack all apply for
free.

Architecture in one picture:

```
  user app                  hsh-tunneld (this binary)             hoop gateway
  ─────────                 ──────────────────────────────       ────────────
  psql -h pg-prod.hoop ─┐
                        │  TUN ↔ gVisor netstack ↔ DNS
                        │       │
                        │       ▼ ACCEPT(127.0.0.1.. → fd…:pg-prod IP:5432)
                        │   ┌─────────────────────┐
                        │   │ client.DialAndPipe()│  ──gRPC──▶  Transport.Connect
                        │   └─────────────────────┘             (one stream / flow)
                        │            ▲                                │
                        └────────────┘                                ▼
                                                                hoop agent → upstream
```

**Linux** (stock `/dev/net/tun`) and **macOS** (stock `utun`) are both
supported. Windows (Wintun) TUN setup is still tracked under Phase 2.

## Layout

| Path                  | What lives there                                              |
|-----------------------|---------------------------------------------------------------|
| `addressing/`         | Deterministic name → dual-stack (ULA IPv6 + CGNAT IPv4) allocator (ADR-0001). |
| `resolver/`           | DNS resolver bound inside the netstack.                       |
| `netstack/`           | gVisor stack + TUN device wiring (Linux `/dev/net/tun`, macOS `utun`). |
| `resolved/`           | Host DNS routing for `*.<tld>` (Linux systemd-resolved, macOS `/etc/resolver`). |
| `service/`            | System-service install/uninstall (Linux systemd, macOS LaunchDaemon). |
| `client/pipe.go`      | Per-flow gRPC pipe; sends SessionOpen + TCPConnectionWrite.   |
| `client/connections.go` | Lists tunnelable connections via `GET /api/connections`.    |
| `cmd/hsh-tunneld/`    | Daemon binary entry point.                                    |

## Running standalone (dev / integration)

You need one terminal and either Linux or macOS. The daemon needs to open
a TUN device, which requires elevated privileges:

- **Linux**: run with `sudo`, or grant `CAP_NET_ADMIN` once with
  `setcap cap_net_admin+ep` on the binary.
- **macOS**: run with `sudo` (opening a `utun` device via the
  PF_SYSTEM control socket requires root). There is no capability
  equivalent; the LaunchDaemon install runs the daemon as root.

The same gateway you already run locally (the OSS dev gateway via
`make run-dev`) is used as-is.

### 1. Build

```bash
make build-hsh-tunneld           # builds for the host platform into dist/
# or, for ad-hoc development:
go build -o /tmp/hsh-tunneld ./tunnel/cmd/hsh-tunneld
```

### 2. Configure

Set the same env vars the `hoop` CLI honors:

```bash
export HOOP_APIURL=http://127.0.0.1:8009
export HOOP_TOKEN=<your bearer token>
# HOOP_GRPCURL is optional; if unset the daemon discovers it from
# GET /api/serverinfo, same as `hoop login`.
```

`hoop login` writes these to `~/.hoop/config.toml`; you can `eval` them
out of there or source them yourself.

### 3. Start the daemon

```bash
sudo -E /tmp/hsh-tunneld
```

Expected output (truncated):

```
hsh-tunneld gateway gRPC 127.0.0.1:8010 (insecure=true) api http://127.0.0.1:8009
hsh-tunneld session prefix fd5a:1b2c:3d4e::/48 gateway fd5a:1b2c:3d4e::1
hsh-tunneld loaded 2 tunnelable connection(s):
hsh-tunneld   pg-prod.hoop (postgres, port 5432) -> fd5a:1b2c:3d4e::a1b2:c3d4
hsh-tunneld   mysql-stg.hoop (mysql, port 3306)  -> fd5a:1b2c:3d4e::e5f6:0708
hsh-tunneld tunnel is up.
hsh-tunneld   host addr: fd5a:1b2c:3d4e::2 (tun0)
hsh-tunneld   resolver:  fd5a:1b2c:3d4e::1:53 (gVisor)
hsh-tunneld   try:       dig @fd5a:1b2c:3d4e::1 pg-prod.hoop AAAA

hsh-tunneld To route *.hoop through this resolver host-wide (systemd-resolved):
hsh-tunneld   sudo resolvectl dns tun0 fd5a:1b2c:3d4e::1
hsh-tunneld   sudo resolvectl domain tun0 '~hoop'
hsh-tunneld After that:  psql -h pg-prod.hoop ...   (or any *.hoop host)
```

### 4. Verify (without changing host DNS)

```bash
# Direct DNS query — must return the allocated AAAA.
dig @fd5a:1b2c:3d4e::1 pg-prod.hoop AAAA

# Direct connection — IP from the dig output:
psql -h fd5a:1b2c:3d4e::a1b2:c3d4 -U noop
```

### 5. Verify (with host DNS routing)

The daemon wires host DNS automatically on bring-up, so on a stock
system `*.hoop` already resolves and you can skip straight to `psql -h
pg-prod.hoop`:

- **Linux (systemd-resolved)**: the daemon runs `resolvectl dns/domain`
  on the TUN interface. If you need to do it by hand (e.g. resolved
  isn't running), the banner prints the exact commands. The device name
  is kernel-assigned (`tun0`, `tun1`, …).
- **macOS**: the daemon writes `/etc/resolver/hoop` pointing
  mDNSResponder at the in-stack resolver; no manual step. The device
  name is kernel-assigned (`utun3`, `utun7`, …). To inspect it:
  `cat /etc/resolver/hoop` and `scutil --dns | grep -A3 hoop`.

```bash
# Linux, manual fallback only:
sudo resolvectl dns tun0 fd5a:1b2c:3d4e::1
sudo resolvectl domain tun0 '~hoop'

psql -h pg-prod.hoop -U noop
```

Both go through the existing gateway. Watch the gateway logs to see
per-flow session open / close events tagged with the connection name.

## Which connections are tunnelable?

Only TCP-style protocols: **postgres, mysql, mssql, mongodb, oracledb,
tcp**. SSH, HTTP-proxy, kubernetes, RDP, SSM, and command-line
connections need protocol-specific clients (or interactive shells) and
are intentionally filtered out of the resolver. Use `hoop connect <name>`
for those.

## Addressing (dual-stack)

Every connection is allocated **two** stable virtual IPs from a
deterministic, session-seeded map:

- An **IPv6 ULA** address inside a per-session `fd00::/8` `/48`.
- An **IPv4 CGNAT** address inside a per-session `100.64.0.0/10` `/16`
  (RFC 6598 shared-address space — like Tailscale uses; it won't collide
  with typical RFC 1918 LANs).

The in-stack resolver answers **both** `AAAA` (v6) and `A` (v4) for each
name, and the netstack accepts TCP flows over either family.

Why dual-stack and not v6-only: macOS `getaddrinfo()` honours
`AI_ADDRCONFIG` and refuses to query/return `AAAA` for hostnames when the
host has no globally-routable IPv6 address (the common case — only the
tunnel provides v6). A v6-only answer is therefore invisible to real apps
on macOS (`psql -h foo.hoop` fails) even though `dig`/`ping6` work. IPv4
has no such gating, so handing out an A record makes resolution work
everywhere with no per-OS divergence. See ADR-0001 for the full rationale.

## Flags

| Flag        | Default          | Meaning                                                |
|-------------|------------------|--------------------------------------------------------|
| `-tld`      | `hoop`           | Tunnel TLD (also `$HSH_TUNNEL_DOMAIN`).                |
| `-dev`      | _kernel-picked_  | Requested TUN device name (`tunN` on Linux, `utunN` on macOS). |
| `-session`  | `spike-session`  | Session seed (drives the `/48` and the deterministic IPs). |

## Limitations (v1)

- **No reconnect.** Each TCP flow's gRPC stream is independent; a flow
  whose stream dies must be re-initiated by the application. The daemon
  process itself does not need reconnect logic (see RD-209).
- **No UDP.** TCP only.
- **`ping` works over IPv6 but not IPv4.** The netstack runs in gVisor's
  promiscuous/spoofing mode (it owns every connection IP on demand rather
  than assigning thousands of addresses). gVisor's IPv6 stack answers
  ICMPv6 echo for these on-demand addresses; its IPv4 stack deliberately
  does not reply to ICMPv4 echo for "temporary" (spoofed) addresses. So
  `ping6 foo.hoop` succeeds but `ping foo.hoop` (which resolves to the v4
  address) times out. This is cosmetic: the tunnel is **TCP-only**, so
  test connectivity with `nc -z foo.hoop <port>` or the actual client
  (`psql`/`mysql`/…), not `ping`. Implementing a custom ICMPv4 echo
  responder was judged not worth the complexity for a non-data-path poke.
- **Linux + macOS.** Windows (Wintun) TUN setup is not implemented yet.
- **Permissions.** The daemon runs as root: it opens `/dev/net/tun`
  (Linux, `CAP_NET_ADMIN`) or a `utun` device (macOS, PF_SYSTEM control
  socket) and mutates the host route table + DNS config. When installed
  via `hsh install` this is handled by the system service (systemd unit /
  LaunchDaemon).
- **Host DNS routing is automatic** on Linux (systemd-resolved) and macOS
  (`/etc/resolver`). Hosts without systemd-resolved fall back to the
  manual hint printed in the bring-up banner.
- **JIT review per flow.** Connections requiring access review will fail
  fast on the tunnel: there is no user-facing UI per TCP connection.
  Run `hoop connect <name>` once to request access out-of-band.

## Tests

```bash
go test ./tunnel/...           # unit tests for addressing/resolver
go test ./tunnel/... -race     # all pass with the race detector
```

The TUN device, gVisor netstack, and the gRPC pipe itself are not
exercised by unit tests — they need a running gateway + agent. The
manual run above is the integration test for now.

## See also

- [`docs/adr/0001-tunnel-addressing.md`](../docs/adr/0001-tunnel-addressing.md) — locked-in design decisions.
- [hoophq/hsh](https://github.com/hoophq/hsh) — the unprivileged user-facing CLI / tray that drives this daemon.
- RD-214 — daemon rename and release-train integration.
- RD-215 / RD-216 — local IPC control plane and OAuth flow (in flight).
