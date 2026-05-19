# tunnel/ — Hoop Tunnel spike (RD-176)

A single-process tunnel client + stub gateway that proves the locked-in
architecture works end-to-end:

- Client speaks WebSocket to the gateway.
- Userspace TCP/IP stack (gVisor) attached to a Linux TUN device.
- A `/48` IPv6 ULA prefix derived from the session seed; every connection
  name gets a deterministic IP inside it.
- Local DNS resolver bound to the gateway IP inside the netstack.
- TCP streams are framed `(stream_id, connection_name, bytes)` and
  multiplexed over the single WebSocket.

The gateway side is a stub that forwards each opened stream to a hardcoded
`host:port`. Real gateway integration is RD-179 / RD-180.

This is currently **Linux-only**. macOS and Windows support are tracked
separately under Phase 2.

## Layout

| Path                  | What lives there                                        |
|-----------------------|---------------------------------------------------------|
| `wire/`               | Binary framing used on the WebSocket.                   |
| `addressing/`         | Deterministic name → ULA IPv6 allocator (ADR-0001).     |
| `client/`             | `Session` (one WS conn) and `Stream` (one TCP-like).    |
| `resolver/`           | DNS resolver bound inside the netstack.                 |
| `netstack/`           | gVisor stack + TUN device wiring (Linux only).          |
| `stub/`               | Stub gateway: WS upgrade + per-name TCP forwarder.      |
| `cmd/stub-gateway/`   | Standalone stub server binary (testing only).           |
| `cmd/hsh-tunnel/`     | Spike client binary (will be folded into `hsh` later).  |

## Running the spike

You need two terminals and Linux. The client needs `CAP_NET_ADMIN` to open
the TUN device — either run with `sudo` or `setcap cap_net_admin+ep` the
binary once.

### 1. Build

```bash
go build -o /tmp/stub-gateway ./tunnel/cmd/stub-gateway
go build -o /tmp/hsh-tunnel    ./tunnel/cmd/hsh-tunnel
```

### 2. Start the stub gateway

Point each connection name at a real local service. Two common examples:

```bash
# Local Postgres on 5432, local SSH on 22:
/tmp/stub-gateway -listen :7575 \
  -targets 'pg-prod=127.0.0.1:5432,bastion=127.0.0.1:22'
```

The stub serves:

- `GET  /api/tunnel/connections` — JSON list, used by the client to populate
  the resolver.
- `WS   /api/tunnel`             — the tunnel WebSocket.

### 3. Start the tunnel client

```bash
sudo /tmp/hsh-tunnel -gateway ws://127.0.0.1:7575/api/tunnel
```

Expected output:

```
hsh-tunnel session prefix fd5a:1b2c:3d4e::/48 gateway fd5a:1b2c:3d4e::1
hsh-tunnel loaded 2 connection(s):
hsh-tunnel   pg-prod.hoop -> fd5a:1b2c:3d4e::a1b2:c3d4
hsh-tunnel   bastion.hoop -> fd5a:1b2c:3d4e::e5f6:0708
hsh-tunnel tunnel session opened
hsh-tunnel tunnel is up.
hsh-tunnel   resolver:  fd5a:1b2c:3d4e::1:53
hsh-tunnel   try:       dig @fd5a:1b2c:3d4e::1 pg-prod.hoop AAAA

hsh-tunnel To route *.hoop through this resolver host-wide (systemd-resolved):
hsh-tunnel   sudo resolvectl dns tun0 fd5a:1b2c:3d4e::1
hsh-tunnel   sudo resolvectl domain tun0 '~hoop'
hsh-tunnel After that:  psql -h pg-prod.hoop ...   (or any *.hoop host)
```

### 4. Verify (without changing host DNS)

```bash
# Direct DNS query — must return the allocated AAAA.
dig @fd5a:1b2c:3d4e::1 pg-prod.hoop AAAA

# Direct connection — IP from the dig output:
psql -h fd5a:1b2c:3d4e::a1b2:c3d4 -U postgres
```

### 5. Verify (with host DNS routing)

```bash
sudo resolvectl dns tun0 fd5a:1b2c:3d4e::1
sudo resolvectl domain tun0 '~hoop'

psql -h pg-prod.hoop -U postgres
ssh me@bastion.hoop
```

Both go through the WebSocket tunnel. Watch `/tmp/stub-gateway`'s stderr to
see the per-stream dial events.

## Flags

| Binary         | Flag             | Default                       | Meaning                              |
|----------------|------------------|-------------------------------|--------------------------------------|
| `stub-gateway` | `-listen`        | `:7575`                       | Address to listen on.                |
| `stub-gateway` | `-targets`       | _empty_                       | Comma-separated `name=host:port`.    |
| `stub-gateway` | `-targets-file`  | _empty_                       | File of `name=host:port` lines.      |
| `hsh-tunnel`   | `-gateway`       | `ws://127.0.0.1:7575/api/tunnel` | Gateway WebSocket URL.            |
| `hsh-tunnel`   | `-tld`           | `hoop`                        | Tunnel TLD (also `$HSH_TUNNEL_DOMAIN`). |
| `hsh-tunnel`   | `-dev`           | _kernel-picked_               | Requested TUN device name.           |
| `hsh-tunnel`   | `-session`       | `spike-session`               | Session seed (drives the `/48`).     |

## Limitations (spike only)

- **No auth.** The stub gateway accepts any WebSocket upgrade.
- **No reconnect.** WS drop = all streams die.
- **No UDP.** TCP only.
- **Linux only.** macOS/Windows TUN setup not implemented.
- **Permissions.** The client needs `CAP_NET_ADMIN` to open `/dev/net/tun`.
- **Host DNS routing is manual.** RD-208 ships an automatic per-platform
  flow.
- **Read deadlines on `Stream`** are no-ops. gVisor's TCP code expects them
  for `time.Time{}` (cancel deadline) but doesn't break on no-ops; full
  deadline support is a follow-up.

## Tests

```bash
go test ./tunnel/...           # unit + WS integration tests
go test ./tunnel/... -race     # all pass with the race detector
```

The TUN device itself is not exercised by tests — it needs root and `tun0`
on the kernel side. The wire / addressing / resolver / session layers cover
the moving parts that have determinism we can pin down.

## See also

- [`docs/adr/0001-tunnel-addressing.md`](../docs/adr/0001-tunnel-addressing.md) — locked-in design decisions.
- RD-176 — the spike ticket.
- RD-204 — the addressing ADR ticket.
