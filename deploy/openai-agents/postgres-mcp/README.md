# OpenAI Agents API: Postgres through an MCP server and the sidecar

An agent in an OpenAI Agents API hosted sandbox queries Postgres with an MCP
tool. The MCP server reaches the database only through a hoop sidecar, which
refuses writes, masks emails and records each statement.
`deploy/managed-agents/postgres-mcp` is the Claude twin. The docs page:
[Postgres through an MCP server](https://hoop.dev/docs/integrations/openai-agents#postgres-through-an-mcp-server).

```
OpenAI's servers            your host
connectors list    HTTPS   +--------+       +-------+  pgwire  +--------------+       +----------+
MCP tool call ------------>| caddy  |------>| dbhub |--------->| hoop sidecar |------>| Postgres |
vault: token               | list,  |  /mcp | MCP   |          | refuse, mask,|       +----------+
                           | TLS    |       |       |          | audit        |
                           +--------+       +-------+          +--------------+
```

A hosted sandbox cannot open a TCP connection to a database, so the agent
gets Postgres as an MCP tool. OpenAI's servers make the MCP call, not the
sandbox, and the sandbox's network is `disabled`.

There is no route around the sidecar. dbhub, the MCP server, shares a
network with Caddy and with the sidecar, and with nothing else: it has no
route to Postgres or to the internet.

## Why `service` origin

The agent's MCP tool sets `connection_origin: service`:

- The vault sends dbhub's token from OpenAI's servers, not from the sandbox.
- With `required: true`, a turn fails when dbhub cannot be reached. A plugin
  MCP server, the `environment` origin, has no `required` and fails silently
  (ENG-864).
- `environment` origin can read a vault variable through a plugin
  `.mcp.json` (ENG-864). OpenAI does not document that.

## Files

| Path | Holds |
|---|---|
| `docker-compose.yml` | Postgres, the sidecar, dbhub 1.5.0 and Caddy, on four networks |
| `Caddyfile` | TLS, OpenAI's IP list, `/mcp` only |
| `sidecar.yaml` | One postgres lane: a read-only rule and an email mask |
| `seed.sql` | A demo `customers` table and dbhub's login |
| `run.sh` | Starts the stack, fetches OpenAI's IP list, writes `.env` with the token |
| `setup.sh` | Creates and deletes the OpenAI side from `openai/`, and starts a session |
| `openai/` | The request bodies: the environment template, the vault, the credential without its secret, the agent (`agent.json`, instructions in `agent.md`), the session |
| `docker-compose.test.yml`, `demo.sh`, `e2e.sh` | Tests: see [Try it](#try-it-without-a-public-host) |

`sidecar.yaml`, `seed.sql`, `docker-compose.test.yml` and three of the
Compose services are the Claude twin's. CI fails when they differ.

## Before you start

- A Linux host with ports 80 and 443 open, and a DNS name for it. Below,
  `MCP_HOST` is that name. Docker Desktop is not supported: it hides the
  caller's address, so every call gets 403.
- Docker Compose 2.24 or later, `curl`, `jq` 1.6 or later and `openssl`.
- An OpenAI API key with Agents API access.

## 1. Run the stack

```bash
MCP_HOST=mcp.example.com ./run.sh
```

Caddy gets a Let's Encrypt certificate for `MCP_HOST`. `.env` holds the
token, `DBHUB_AUTH_TOKEN`; keep it out of git.

`run.sh` writes `ranges/openai.caddy` from
[chatgpt-connectors.json](https://openai.com/chatgpt-connectors.json), and
Caddy admits only those addresses. The list changes. Refresh it every hour,
for example with cron; cron's `PATH` must reach `docker`, `curl` and `jq`:

```
@hourly cd /srv/hoop/postgres-mcp && ./run.sh ranges
```

A failed fetch stops `run.sh` and leaves the last list in place.

To use your own database:

- Remove the `postgres` service and the sidecar's `depends_on`, and set
  `upstream` in `sidecar.yaml`.
- Put the sidecar, and only the sidecar, on a network that reaches the
  database. Across a network you do not trust, set `upstream_tls` on the
  lane (sidecar README, "Upstream TLS").
- Set dbhub's `DSN` to a login like the `agent` role in `seed.sql`: SELECT
  only, no TEMPORARY, no large-object creation, read-only by default, and a
  `statement_timeout`. Replace the demo passwords.

## 2. Create the OpenAI side

`setup.sh` reads `MCP_HOST` and the token from `.env`, so run it in this
directory on the stack's host.

```bash
read -rs OPENAI_API_KEY && export OPENAI_API_KEY
./setup.sh create --dry-run    # the request bodies, with your host; sends nothing
./setup.sh create
```

`create` makes an environment template with `network.access: disabled`, a
vault with dbhub's token for `https://MCP_HOST/mcp`, and the agent. It
writes their ids to `./.openai-ids`, and refuses a second run until
`delete`.

The vault sends the token as `Authorization: Bearer <token>` to the MCP
server whose URL matches the credential's.

## 3. Start a session

```bash
./setup.sh session 'Which plan is each customer on?'
```

The script waits for the turn and prints the agent's tool calls and answer.

## 4. Read the audit trail

```bash
docker compose logs --no-log-prefix sidecar | grep '^{' \
  | jq -c 'select(.direction == "client") | {kind, operation, allowed, rule, statement}'
```

## 5. Clean up

```bash
./setup.sh delete
./run.sh down
```

`delete` removes the vault, the sessions, the agent and the template. If it
fails halfway, run it again: it retries only what is left.

## What to expect

| The agent | Result |
|---|---|
| Runs a SELECT | Rows, with emails as `[REDACTED:EMAIL_ADDRESS]` |
| Runs a write, also in a CTE, `MERGE`, `DO`, `COPY FROM` or `EXPLAIN ANALYZE` | A failed tool call: `this connection is read-only; writes are not permitted`. Postgres never receives it. It can restart dbhub: see [Limits](#limits). |
| Any statement | One audit row, direction `client`, principal `agent` |
| A caller outside the connectors list | `403 forbidden` from Caddy |
| No token, or a wrong one | `401` from dbhub |

## Try it without a public host

```bash
./run.sh --test && ./demo.sh    # every claim above, no OpenAI call
./run.sh --tunnel && ./e2e.sh   # a real session through a Cloudflare quick tunnel
./run.sh down
```

Both need `python3`. In both modes Caddy takes the caller's address from
`Cf-Connecting-Ip`, and trusts it only from the tunnel's own subnet. A quick
tunnel is for tests: it has no uptime guarantee and a new name on each run.
`e2e.sh` writes the transcript, audit rows and Caddy log to `evidence/`.

## Limits

OpenAI's:

- **The list is a measurement.** OpenAI names no list for Agents API MCP
  calls. Four sessions (ENG-860, ENG-866) called from `9.129.0.0/16`,
  `20.228.106.176/28`, `57.154.174.112/28` and `20.125.112.224/28`, all in
  `chatgpt-connectors.json`; only the first is in `chatgpt-agents.json`. If
  OpenAI moves the calls, they get 403 and the turn fails.
- **The list holds every OpenAI customer.** It proves only that the call came
  from OpenAI. The token is what admits your agent.
- **No mTLS.** OpenAI documents mutual TLS for ChatGPT plugins only. It is
  untested here: a quick tunnel ends TLS at Cloudflare.
- **US data residency only, no ZDR.** The Agents API keeps data in the
  United States only and does not support Zero Data Retention
  ([Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview)).
  The masked rows the agent reads go to OpenAI.

The template's:

- **A refusal closes the connection.** The sidecar sends `FATAL 42501` and
  closes it. dbhub's pool opens a new one, and the next statement works.
  ENG-800 tracks a refusal that keeps the connection open.
- **A refused write can restart dbhub.** dbhub 1.5.0 exits on the closed
  connection when the write is in a multi-statement call, or when another
  call runs at the same time (bytebase/dbhub#479, fixed in dbhub but not yet
  released). Docker restarts it. The calls in flight get no answer, and other
  calls fail until dbhub is healthy.
- **The mask matches text that looks like an email.** A statement that
  reshapes the value, such as `replace(email, '@', ' at ')`, gets it out
  unmasked. Keep a column that must not leave out of the login's grants.
- **The read-only rule depends on the SQL classifier.** Some statements
  that write pass as reads (ENG-803), and so do `LOCK TABLE`, `SELECT ...
  FOR UPDATE` and a function with side effects. The login's grants are
  what stop them, so keep them as narrow as `seed.sql`.
- **The login's settings are defaults.** The rule refuses `SET`, but
  `set_config()` in a SELECT can still lift the read-only default or a
  timeout for the session. The grants still hold.
- **Error messages are not masked.** A cast error can quote a value, such
  as `invalid input syntax for type integer: "ada@example.com"`, to the
  agent and into the audit trail (ENG-805).
- **No OpenAI session in the audit.** dbhub uses one login and one pool, so
  each row names `agent`, not a session or a person.
- **Free tier.** One guardrail rule and one mask rule per process. A license
  lifts both caps.
- **Audit rows hold SQL text.** A literal in a statement is recorded as
  written. Masking applies to results.
