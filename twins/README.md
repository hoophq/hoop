# twins (ENG-690 spike, throwaway)

Behavioral clones of the third-party services hoop calls, and one scenario
that drives the real `hoop` binary against them. Not merge quality: the ADR
from this spike decides the real layout.

```
hoop exec ──gRPC──> hoop start standalone ──HTTPS──> oidc twin   (issuer https://oidc-twin)
   (CLI)            gateway + PGlite + agent  ──HTTPS──> slack twin (slack.com, wss-primary.slack.com, hooks.slack.com)
                                                   A: compose aliases   B: HTTPS_PROXY -> twin router
scenario (go test) ──HTTP──> /api/*  and  /_twin/seed|state|reset|faults|act/*
```

## Run it

```bash
# needs Docker and an enterprise license (review on exec is refused on OSS)
TWINS_LICENSE_FILE=/path/license.json make test-twins-scenario                 # A
TWINS_REACH=proxy TWINS_LICENSE_FILE=/path/license.json make test-twins-scenario  # B
TWINS_REACH=native ...   # B with no Docker, Linux only (darwin ignores SSL_CERT_FILE)
```

`TWINS_KEEP=1` leaves the stack up. `TWINS_ARTIFACTS=dir` keeps the twin
states, `compose.log` and `timings.txt`.

## What is real and what is faked

| Piece | Here | In production |
|---|---|---|
| hoop gateway, agent, CLI | real binary from this branch, unmodified | the same |
| database | embedded PGlite (`hoop start standalone`) | PostgreSQL |
| IdP | OIDC twin: discovery, authorize, token, userinfo, JWKS; RS256 keys minted at start | Okta, Auth0, Entra, Google... |
| login | the scenario follows the redirects over HTTP; `login_hint` picks the seeded user | a browser and a password |
| Slack Web API | Slack twin on `slack.com`: `auth.test`, `apps.connections.open`, `chat.postMessage`, `chat.update`, `chat.postEphemeral`, `users.info`, `views.open` | slack.com |
| Socket Mode | Slack twin on `wss-primary.slack.com`: `hello`, pings every 10s, `interactive` envelopes, acks | Slack |
| the reviewer | `POST /_twin/act/click` sends `block_actions` as a seeded Slack user | a person clicking |
| TLS | a test CA minted per run, trusted through `SSL_CERT_FILE` only | public CAs |
| DNS | A: compose aliases. B: the router maps hostnames; nothing resolves | public DNS |

Not covered: reject with a reason (modal submission), `response_url` (the
approve path rewrites a tracked message with `chat.update`), slash
commands, Slack rate limits, OIDC refresh and group sync, SAML, contract
tests against real accounts. Failure injection exists in the kit
(`POST /_twin/faults`) but no scenario uses it yet.
