# twins

Behavioral clones ("twins") of the third-party services hoop calls, and the
scenarios that drive the real `hoop` binary against them with no real
account. ADR-0032 (hoophq/adr) decides the layout.

```
scenario (go test) ──HTTP──> /api/*  of  hoop start standalone ──HTTPS_PROXY──> router ──> oidc twin
        └──────────HTTP──> /_twin/seed|state|reset|faults        (gateway, PGlite, agent)        (issuer https://oidc-twin)
```

- `kit/`: what every twin shares. The admin API, failure injection, the
  test PKI, the TLS and admin listeners.
- `oidc/`: the OIDC provider twin and its contract suite.
- `router/`: the `HTTPS_PROXY` the gateway uses. It tunnels a twin's
  hostname to the twin and refuses any other host.
- `scenario/`: the runner and the scenarios (build tag `scenario`).
- `cmd/twins/`: one binary, `twins pki|oidc|router`.

One module, `github.com/hoophq/hoop/twins`, outside `go.work`. It imports
nothing from hoop: the scenarios drive the binary over HTTP.

## Run it

```bash
make test-twins            # kit, router and twin tests, contract suites
make test-twins-scenario   # boots hoop against the twins; Linux natively, Docker elsewhere
```

| Variable | Effect |
|---|---|
| `TWINS_REACH=native\|proxy` | `native`: every process on this host, Linux only, because darwin ignores `SSL_CERT_FILE`. `proxy`: the same stack under docker compose. Default: `native` on Linux, `proxy` elsewhere. |
| `HOOP_BIN_DIR=dir` | Use `dir/hoop` and build none. A linux binary for `proxy`. |
| `TWINS_ARTIFACTS=dir` | Keep `oidc-state.json`, `router-state.json`, `timings.txt` and the logs. |
| `TWINS_KEEP=1` | Leave the compose stack up. |

The native stack uses these ports: the gateway listens on all interfaces at
18009 (API) and 8010 (gRPC, fixed by the gateway); the OIDC twin at
`127.0.0.1:18081` (admin) and `127.0.0.1:18441` (TLS); the router at
`127.0.0.1:18083`. One native stack runs per host. A native run deletes
`<TWINS_ARTIFACTS>/native` first, so it always boots on an empty database.

At boot, the stack logs alice in first: she is the org's first user, so
she is admin. Her token lets the runner wait for the standalone agent to
report `CONNECTED` before the first scenario. A scenario's own users log in
after her.

After its last scenario, a stack waits 10s, then fails on any host the
gateway tried that no twin answers. `knownEgress` in `scenario/stack_test.go`
lists the accepted hosts, each with its reason.

## The admin API

Every twin serves it on its admin port (plain HTTP) and on its TLS port.

| Route | Effect |
|---|---|
| `POST /_twin/seed` | Load state. More calls add to it. |
| `GET /_twin/state` | What a scenario asserts on, plus the faults and their hits. |
| `POST /_twin/reset` | Drop all state and faults. |
| `POST /_twin/faults` | `{"path": "/jwks", "status": 503, "body": "", "delay_ms": 0, "times": 0}`. Matches a path prefix. `times: 0` means until cleared. |
| `DELETE /_twin/faults` | Drop the faults, keep the state. |
| `GET /_twin/healthz` | `ok`. |

OIDC twin seed fields: `users` (`sub`, `email`, `name`, `groups`; the same
email replaces the user), `groups_claim` (default
`https://app.hoop.dev/groups`), `client_id` and `client_secret` (default
`hoop`, `hoop-secret`), `token_ttl_sec` (default 3600), and
`expired_tokens`: `["id_token"]` or `["access_token"]` makes the token
endpoint issue them already expired; `[]` stops it. 429, 5xx and latency
are kit faults on the endpoint path. `/_twin/state` returns the users, the
codes and the tokens issued (kind, subject, grant, expiry, last 8
characters).

## OIDC contract suite

`oidc/contract_test.go` checks what hoop needs from a provider: discovery,
JWKS, token endpoint errors, refresh, userinfo with the groups claim. It
runs against an in-process twin. Set these to run it against a real issuer
instead; a difference means the twin is wrong:

| Variable | Value |
|---|---|
| `TWINS_OIDC_ISSUER` | The issuer URL, as hoop is configured. |
| `TWINS_OIDC_CLIENT_ID`, `TWINS_OIDC_CLIENT_SECRET` | A confidential client of that issuer. |
| `TWINS_OIDC_REFRESH_TOKEN` | A refresh token of a user in at least one group, from that client. Turn off refresh token rotation, or the next run fails. |
| `TWINS_OIDC_GROUPS_CLAIM` | Optional. Default `https://app.hoop.dev/groups`. |

```bash
TWINS_OIDC_ISSUER=https://example.okta.com ... make test-twins
```

## What is real and what is faked

| Piece | In a scenario | In production |
|---|---|---|
| hoop gateway, agent, CLI | the real binary from this branch, unmodified | the same |
| database | embedded PGlite (`hoop start standalone`) | PostgreSQL |
| IdP | OIDC twin: discovery, authorize, token (`authorization_code`, `refresh_token`), userinfo, JWKS. RS256 key minted at start | Okta, Auth0, Entra, Google... |
| gateway config | `IDP_ISSUER`, `IDP_CLIENT_ID`, `IDP_CLIENT_SECRET` env | env or `PUT /api/serverconfig/auth` |
| login | the scenario follows the redirects over HTTP; `login_hint` names the seeded user, and authorize approves with no prompt | a browser, a password, MFA |
| users and groups | `/_twin/seed` | the IdP directory |
| TLS | a test CA minted per run, trusted through `SSL_CERT_FILE` only, which replaces the system roots | public CAs |
| DNS | none: the router maps the twin's hostname | public DNS |
| egress | the router refuses any host with no twin and records it | the internet |

## Not covered

- A browser login and the CLI `hoop login`, which listens on `127.0.0.1:3587` (ENG-702).
- SAML (ENG-695).
- Vendor directory group APIs (Auth0, Entra Graph, Google `cloudidentity`): no handler calls `ListDirectoryGroups`.
- Runtime config through `PUT /api/serverconfig/auth`.
- Opaque access tokens and `_userinfo=1`: the twin issues JWT access tokens only.
- Registered redirect URIs, consent, MFA and `prompt`: authorize approves any absolute `redirect_uri`.
- Refresh token rotation and revocation: a refresh token stays valid until reset.
- Key rotation: one signing key per run.
- PKCE and `nonce` checks: the gateway sends neither. The twin echoes a `nonce` it receives.
- Released agents (ENG-698) and the nightly contract run against sandbox accounts (ENG-699).
