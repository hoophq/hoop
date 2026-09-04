<h1 align="center">Runtime control for agents</h1>

<p align="center">
Data and context make agents useful. Runtime controls make them safe.<br>
An open-source sidecar. One binary, one config file, MIT.
</p>

<p align="center">
<a href="https://github.com/hoophq/hoop/releases"><img src="https://img.shields.io/github/v/release/hoophq/hoop?style=flat-square" alt="release"></a>
<a href="https://github.com/hoophq/hoop/blob/main/LICENSE"><img src="https://img.shields.io/github/license/hoophq/hoop?style=flat-square" alt="license"></a>
<a href="https://hub.docker.com/r/hoophq/hoop"><img src="https://img.shields.io/docker/pulls/hoophq/hoop?style=flat-square" alt="docker pulls"></a>
<a href="https://github.com/hoophq/hoop/stargazers"><img src="https://img.shields.io/github/stars/hoophq/hoop?style=flat-square" alt="stars"></a>
</p>

## Install

```bash
brew tap hoophq/brew https://github.com/hoophq/brew.git
brew install hoop
```

## Configure

One file. This one redacts emails and refuses destructive SQL.

```yaml
log_level: info

admin:
  listen: 127.0.0.1:19000        # /healthz /stats /config /events


mask:                            # Data Masking
  enabled: true
  rules:
    - {name: emails, entity: EMAIL_ADDRESS, strategy: redact}

policy:                          # Guardrails
  enforce: true
  rules:
    - name: no-destructive-sql
      type: operation
      operations: [drop, delete, truncate]
guardails:
  rules:
    - name: no-destructive-sql
      type: operation
      operations: [drop, delete, truncate]
      message: destructive statements are not permitted

listeners:
  - name: localdb
    protocol: postgres           # postgres | mssql | http
    listen: 127.0.0.1:15432      # where clients connect
    upstream: 127.0.0.1:5432     # your real resource
    connection: localdb
```

## Run

```bash
hoop start sidecar --config config.yaml           # start
hoop start sidecar --config config.yaml --validate # check the config, then exit
```

Point your agent at `127.0.0.1:15432` instead of the database. Your agents change one thing: the port in their connection string. The sidecar runs next to your database, not in place of it.

## What Happens to a Query

**The query runs. The sensitive fields stay hidden.**

```
> pull the customer list for the churn report

● postgres(SELECT name, email FROM customers)
  ⎿  name           email
     Ada Lovelace   [REDACTED:EMAIL_ADDRESS]
     Grace Hopper   [REDACTED:EMAIL_ADDRESS]
     hoop.dev · emails redacted at the wire
```

Masking rewrites the response in memory. The request itself is never touched.

**The statement never reaches the database.**

```
> clean up the old test rows in customers

● postgres(DELETE FROM customers WHERE id = 1)
  ⎿  FATAL:  destructive statements are not permitted
     hoop.dev · guardrail: no-destructive-sql
```

A real pgwire error carrying the message you wrote in the config. The agent reads why it was refused instead of guessing at a dropped connection.

The agent never knows the sidecar exists. No SDK, no prompt changes, no agent-side config.

## The Four Controls

**Data Masking.** Rewrites sensitive values in the response, in memory, before they reach the client. The request itself is never touched.

**Guardrails.** An ordered deny list checked against every statement. Destructive ones never reach the database, and the client reads the reason you wrote.

**Session Analyzer.** An agent scores every action's intent and syntax for risk before it executes. Local rules run first, so a statement a guardrail already refuses never costs a model call.

**Reviews.** Risky operations are escalated to a human for one-off approval.

## Supported Protocols

| Protocol | Masking | Guardrails | Session Analyzer |
| --- | --- | --- | --- |
| PostgreSQL | yes | yes | yes |
| Microsoft SQL Server | yes | yes | yes |
| HTTP | yes | yes | yes, with `http.capture_body` |

Wire protocols outlive models, frameworks, and MCP. The sidecar works behind whatever interface your agent uses.

## The Control Plane

One sidecar runs from one config file. A fleet needs one place to see them all.

The control plane is the admin surface for that. Connect your sidecars. Set Data Masking, Guardrails, and the Session Analyzer once for all of them. Work the Reviews queue when an operation needs a person. Admins sign in here.


| Surface | State |
| --- | --- |
| Guardrails, Data Masking, Session Analyzer, Review rules | Built. Configuration lives in the control plane. |
| Slack for review delivery | Built. |
| Administrators | Built. |
| Sidecar fleet: token issuance, resources, liveness | Not built |
| Review queue: approve, reject, retry | Not built |
| Pushing configuration to the fleet | Not built. Each sidecar still reads its own file. |

The UI is [`webapp_v2/`](webapp_v2/), the same web app the gateway serves: it renders as the control plane when the backend reports `application_mode: "control-plane"`. To run it:

```bash
make run-dev-postgres
make run-dev-control-plane                                      # control plane on :8019
cd webapp_v2 && API_URL=http://localhost:8019 npm run dev       # UI on :5173
```

Routes with no backend behind them say so and name the work they wait on. You will not find an empty table pretending to be loaded.

## Why This Exists

Nobody runs agents in production because they want agents in production. They do it because agents need context and data to be useful.

That risk moves at machine speed. An agent that drops a table is not smart. It is fast. A static rule cannot judge intent, and a human cannot review at machine speed.

So the control has to be an agent too. One that moves as fast as yours, with a single job: read every statement live and stop the dangerous one before it lands.

**Not a PAM.** PAM decides who connects. Once a user is connected, PAM is done. hoop.dev controls what every statement does and what comes back.

**Not an MCP gateway.** MCP gateways broker one interface. The sidecar sits on the wire underneath, so it covers the protocols an MCP server never speaks.

**Not an agent sandbox.** Sandboxes isolate the agent. The sidecar governs the connection between the agent and the resource it needs.

**Not a platform you migrate to.** Keep your identity stack, keep your sandbox, keep what you run. The sidecar rides next to it.

## What Is in This Repository

hoop.dev began as a gateway for human access to infrastructure. The sidecar and the control plane are where the product is going. Both live here.

| Directory | What it is |
| --- | --- |
| `sidecar/` | The inspection core. Wire bytes to statements, statements to verdicts. |
| `client/` | The `hoop` CLI, including `hoop start sidecar`. |
| `gateway/` | The gateway server. REST API on :8009, gRPC on :8010. |
| `agent/` | The agent that runs on your infrastructure and dials the gateway. |
| `agentrs/` | Rust binary for the RDP proxy and TLS termination. |
| `tunnel/` | Client-side tunnel daemon. |
| `common/` | Shared protocol definitions and utilities. |
| `webapp/`, `webapp_v2/` | The gateway web UI. Frozen. |
| `docs/adr/` | Architecture decision records. Start here for the reasoning. |

**The gateway still ships and is still supported.** It covers human access: RBAC, session recording and replay, runbooks, a web terminal, and connectors for Kubernetes, SSH, RDP, and more. If you run it today, nothing changes. [Gateway documentation →](https://hoop.dev/docs)

## Contributing

Policy rule types, masking strategies, protocol coverage, documentation. Start with [the docs](https://hoop.dev/docs), and read [`docs/adr/`](docs/adr/) for how the pieces fit together.

The wire protocol codecs live in a private module (`github.com/hoophq/libhoop`). Building the `sidecar/` module needs `GOPRIVATE=github.com/hoophq/libhoop` and credentials for it. Everything else in this repository builds without them.

Questions and ideas go in [Discussions](https://github.com/hoophq/hoop/discussions).

## License

MIT Licensed.
