# ADR-0019: The sidecar takes the user's identity from the network, not from the database handshake

- **Status:** Proposed
- **Date:** 2026-09-23
- **Author:** @matheusfrancisco
- **Deciders:** TBD
- **Code:** [`sidecar/session/`](../../sidecar/session), [`sidecar/proxy/proxy.go`](../../sidecar/proxy/proxy.go) (`Config.IdentityFn`), [`sidecar/daemon/`](../../sidecar/daemon)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (the relay flow and the Envoy `ext_authz` tier), [ADR-0009](0009-guardrails-and-masking-architecture.md) (the sidecar is for deployments where something else owns identity), [ADR-0013](0013-grpc-terminates-http2-in-process.md) (`identity_header` and SPIFFE peers on the gRPC lane), [ADR-0015](0015-ssh-terminates-at-the-sidecar.md) (the sidecar must attribute a session to a principal it verified itself)
- **Supersedes / Superseded by:** —

## Context

Every audit record and every policy decision the sidecar makes hangs off
`session.Identity` (`sidecar/session/session.go`). Today only three lanes
fill it with something verified: gRPC (an `identity_header` set by a
fronting proxy, or the SPIFFE SAN of an mTLS peer), SSH (a certificate the
sidecar's CA issued, ADR-0015), and nothing else. The database lanes, which
are the reason the sidecar exists, record `anonymous`, with one exception:
the Postgres lane copies the `user` parameter out of the `StartupMessage`
(`proxy/downstream.go`, `startupUser`). That name is a claim the client
made, and the code says so. MySQL parses the username in
`HandshakeResponse41` only to skip past it; MSSQL cannot read it at all
under integrated authentication.

The old model bound a session to a human elsewhere. The hoop agent's
libhoop proxies terminated the client's database authentication with
placeholder credentials (`noop`/`noop`), authenticated upstream with the
secrets stored on the connection, and the gateway recorded which user
owned the session. The human was identified by the gateway's gRPC token,
never by the database handshake. The sidecar has no gateway in that
position: it is a TCP relay that inspects bytes and must run without one.

The clients are stock software the team does not ship: `psql`, `mysql`,
`sqlcmd`, `mongosh`, `curl`, and the drivers behind BI tools and
applications. A stock client has exactly three places to say who it is: the
TLS client certificate, the username and password fields of its protocol's
handshake, and protocol extras such as HTTP headers or the Postgres
`application_name`. Anything else needs custom client software.

Two constraints from the deployments we target:

- The client hop cannot be required to use TLS. Some TLS configurations
  leave the sidecar unable to read the handshake at all, and a design that
  only works when the relay terminates the client's TLS excludes them.
- Whatever fills `Identity` must be something the sidecar verified itself.
  ADR-0015 rejected an SSH design for the sole reason that the sidecar
  would have been attributing sessions to a principal somebody else
  checked. That rule holds here.

The sidecar root module depends on `github.com/hoophq/libhoop` and nothing
else (`sidecar/CLAUDE.md`). Any component that needs another dependency is
a nested module that a binary links or does not.

## Options considered

The options split on one question: where does the identity enter the
connection?

### In the database handshake

1. **The sidecar becomes the authentication server for every protocol.**
   The client connects as `user=<hoop user>` with a bearer in the password
   field; the sidecar answers the handshake, verifies the bearer, then
   authenticates upstream itself. This is the libhoop model with the
   sidecar as the verifier. It is the only handshake-based design where
   the sidecar verifies the human, and it removes database passwords from
   users. It costs a server-side authentication state machine per protocol
   (Postgres cleartext or SCRAM, MySQL `caching_sha2` with the RSA exchange,
   TDS `LOGIN7`, MongoDB SCRAM) plus a client-side one toward the upstream.
   Without client TLS the bearer travels in the clear, so it must be
   single-use and short-lived, or the sidecar must implement the
   challenge-response server with per-user verifiers. MSSQL encrypts
   `LOGIN7` even under `ENCRYPT_OFF`, so "no TLS" does not exist for it;
   Mongo has no plaintext mechanism at all. The per-protocol surface is the
   most safety-critical code in the relay, duplicated once per lane, and
   the hardest to get right.

2. **Trust the claimed username after the upstream accepts it.** Extend
   what the Postgres lane already does to MySQL and MSSQL: read the
   username field, treat it as verified once the upstream replies
   `AuthenticationOk`. No new authentication code. It requires one
   database account per human, which is the arrangement credential
   injection was invented to end, and it reads nothing from MSSQL under
   integrated authentication, where the name lives inside the SSPI ticket.

3. **A token in a non-authentication field.** Postgres `application_name`
   or `options`, MySQL connection attributes, `LOGIN7` `AppName`, Mongo
   `hello` application name. The sidecar peeks, verifies, strips, forwards.
   Field extraction only, but still per protocol, still a bearer on a
   possibly plaintext hop, and the database goes on authenticating the
   user with its own credential.

4. **An in-band first statement.** `SELECT hoop_auth('<token>')` before any
   real work; the gate already sees statements through the codec, and the
   store already lets a principal learned mid-session replace `anonymous`.
   Protocol-agnostic detection, but the reply must be synthesized per
   protocol, the token must never reach the database log, and every
   statement before it needs a policy to refuse it. Two mechanisms for one
   fact.

### In the transport

5. **Mutual TLS with a certificate hoop issues.** `hoop login` writes a
   short-lived X.509; the relay terminates the client's TLS with
   `RequireAndVerifyClientCert`; identity is the SAN, groups an extension,
   mapped the way `SSHIdentityConfig` maps a certificate's fields. Every
   stock client can present a client certificate. Paired with the sidecar
   presenting its own certificate to a database in certificate-auth mode,
   no password exists on either hop and nothing in the handshake is parsed.
   It is the right shape for the deployments that can run it, and it fails
   the first constraint: it requires TLS on the client hop. It also needs
   an identity hook that runs after the relay's TLS negotiation, which for
   Postgres begins only after the client's `SSLRequest`.

6. **The network authenticates the peer; the sidecar asks the network who
   it is.** The sidecar is reachable only over an identity-aware network
   (a tailnet or an equivalent zero-trust overlay) in which every peer
   address belongs to one authenticated node and, through it, one login.
   At accept, before the first byte, the relay resolves the peer address to
   a login through the network's own lookup and refuses the connection when
   the lookup fails or names a machine rather than a person. Identity is
   fixed at the transport layer, so the design covers every protocol the
   relay carries, including MSSQL under integrated authentication and Mongo
   under SCRAM, and a client hop with or without TLS. No handshake is
   parsed; no credential is carried in a protocol field. Its strength is
   exactly the network's guarantee that a peer address maps to one person,
   and it needs the listener to be reachable from nowhere else.

Tailscale is the first provider because the lookup exists and is stable:
`client/local.(*Client).WhoIs(ctx, remoteAddr)` is documented as a stable
API and returns `WhoIsResponse{Node, UserProfile, CapMap}` with
`UserProfile.LoginName` for a human peer, `Node.Tags` and the login
`tagged-devices` for a machine, and `CapMap` carrying ACL grants that
serve as groups without a second identity-provider call. The same call is
one `GET /localapi/v0/whois?addr=` on the tailscaled socket, reachable
from the standard library, so the provider adds no dependency to the root
module. An embedded node (`tailscale.com/tsnet`) is the variant that makes
the listener structurally unreachable from outside the tailnet; it is a
heavy dependency and lives in its own nested module.

### How a Tailscale-fronted lane resolves a session

```mermaid
sequenceDiagram
    participant U as User (psql, tailnet node 100.64.0.7)
    participant TS as tailscaled on the sidecar host
    participant S as sidecar lane appdb :5432
    participant DB as postgres

    Note over U,S: WireGuard tunnel; Tailscale authenticated the user's device and login
    U->>S: TCP connect from 100.64.0.7:51234
    S->>TS: GET /localapi/v0/whois?addr=100.64.0.7:51234 (unix socket)
    alt human peer
        TS-->>S: UserProfile.LoginName alice@corp, Node.Name alice-mbp, CapMap grants
        S->>S: Identity{Subject: alice@corp, Groups: from CapMap, PeerAddr}
        S->>S: session.New; gate.Start; audit session_start principal=alice@corp
        U->>S: StartupMessage user=app_rw
        S->>DB: forwarded as-is; postgres authenticates the connection
        DB-->>S: AuthenticationOk
        S-->>U: AuthenticationOk
        Note over S: every statement attributed to alice@corp; input.context.subject/groups set
    else tagged device (Node.Tags set, LoginName tagged-devices)
        TS-->>S: WhoIsResponse for a machine
        S-->>U: connection refused; no session, no audit row
    else not a tailnet peer, or LocalAPI unreachable
        TS-->>S: 404 / error
        S-->>U: connection refused
    end
```

Where each step lives:

- **Accept.** `proxy.Server.handle` already builds `Identity{PeerAddr}` and
  calls `Config.IdentityFn(conn)` before reading a byte. The Tailscale
  resolver is that function; it gets `conn.RemoteAddr()` and returns the
  identity or an error that closes the socket.
- **Lookup.** One HTTP GET over the tailscaled unix socket
  (`paths.DefaultTailscaledSocket()`, Host `local-tailscaled.sock`),
  decoding `UserProfile.LoginName`, `Node.Name`, `Node.Tags`, `CapMap`
  with `encoding/json`. A per-peer cache with a short TTL keeps a
  reconnecting client from paying it every time.
- **Mapping.** `LoginName` becomes `Subject`; grants in `CapMap` under a
  configured capability name become `Groups`; `Node.Name` lands in
  `Attributes`. A response with `Node.Tags` set is a machine and is refused
  on a human lane, the same rule `sshIdentityRefusal` applies to a
  certificate that names nobody.
- **Bind.** The lane's `listen` address must be one of the host's tailnet
  addresses (LocalAPI `Status().Self.TailscaleIPs`); the config validator
  refuses anything else, so a lane cannot be reached from an interface the
  network did not authenticate.
- **After accept.** Nothing changes. The pumps, gate, codec, policy and
  audit see a session whose principal is verified, the same object the SSH
  and gRPC lanes hand them today.

With the embedded-node variant (`tsnet`), the sidecar process is the
tailnet node: `tsnet.Server.Listen` replaces `net.Listen`, `LocalClient()`
replaces the socket, and the bind rule is structural rather than
validated. The flow above is otherwise identical.

A hoop-operated tunnel that carries a gateway-signed identity assertion was
considered and set aside: it couples the sidecar to the gateway, which the
sidecar exists to run without.

## Decision

We will resolve the identity of a database session from the network the
connection arrived on, at accept time and before the first byte, through a
per-lane resolver that the daemon wires into `proxy.Config.IdentityFn`.

- The resolver contract is `Resolve(ctx, peer net.Addr) (session.Identity,
  error)`. An error, a lookup miss, or a peer that names a machine rather
  than a person refuses the connection. `anonymous` is not a fallback.
- The first provider is Tailscale over LocalAPI `WhoIs`, implemented with
  the standard library in the root module. `tsnet` (embedded node) is a
  nested module a binary may link. Other identity-aware networks with a
  peer lookup join through the same contract.
- A lane that names a network identity source refuses to bind on any
  address the network did not assign; the listener is reachable only
  through the authenticated network, and the config validator rejects
  anything else.
- The database handshake is not parsed for identity. Existing peeks that
  read a claimed username keep their current role: a claim recorded in
  `Metadata`, never promoted to `Subject` on a lane with a verified source.
- The database credential is a separate concern and stays out of this
  decision. The relay forwards the client's authentication as it does
  today; a later decision may pair this with the sidecar authenticating
  upstream by certificate.

## Consequences

Easier:

- Every protocol the relay carries gets a verified principal from the same
  code path, including the ones no handshake design could reach (MSSQL
  with SSPI, Mongo, sessions whose TLS the relay cannot read). Adding a
  protocol adds no identity work.
- Stock clients need nothing: `psql -h appdb` over the tailnet is the whole
  user experience. No certificate to install, no token in an environment
  variable, no client flag.
- The identity carries what the network already knows about the peer:
  login, device, and ACL grants as groups. Policy on `input.context.groups`
  works on day one.
- The sidecar's one-dependency invariant holds: the default provider is a
  unix-socket HTTP call.

Harder, and what we are committed to:

- The guarantee is the network's. A NAT, a shared bastion, a connection
  pooler, or a port-forward between the human and the relay makes that hop
  the principal. The lane refuses to bind anywhere but the network's
  address so the failure is a refused start, not a wrong audit row; the
  operator still has to keep poolers behind the relay, not in front of it.
- Resolution is per TCP connection at accept. A lookup outage refuses
  connections. The resolver caches by peer with a short TTL to keep an
  interactive client from paying the lookup on every reconnect; the cache
  never outlives the network's own session for that node.
- A tagged (machine) node is refused on a human lane. Workload identity is
  a separate lane configuration, not a fallback of this one.
- Identity is bound at connect time. A login revoked mid-session ends at
  the next connection, the same as certificate and token designs.
- The database still authenticates the connection with whatever credential
  the client holds. Removing database passwords from users is the next
  decision, not this one; this decision makes it possible to do that
  without touching identity.

Revisit if: the target deployments turn out not to run an identity-aware
network, in which case option 5 (hoop-issued client certificates) is the
fallback that keeps the "no handshake parsing" property at the cost of
requiring TLS on the client hop; or if a protocol arrives whose clients can
only be reached through a network the relay cannot query.
