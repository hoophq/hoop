# Claude Managed Agents: Postgres through an MCP server and the sidecar

A Claude Managed Agents cloud agent queries Postgres with an MCP tool. The
MCP server reaches the database only through a hoop sidecar, which refuses
writes, masks emails and records each statement.

```
Anthropic's servers         your host
160.79.104.0/21    HTTPS   +--------+       +-------+  pgwire  +--------------+       +----------+
MCP tool call ------------>| caddy  |------>| dbhub |--------->| hoop sidecar |------>| Postgres |
vault: token               | range, |  /mcp | MCP   |          | refuse, mask,|       +----------+
                           | TLS    |       |       |          | audit        |
                           +--------+       +-------+          +--------------+
```

A cloud sandbox cannot open a TCP connection to a database, so the agent
gets Postgres as an MCP tool. Anthropic's servers make the MCP call, not the
sandbox.

There is no route around the sidecar. dbhub, the MCP server, shares a
network with Caddy and with the sidecar, and with nothing else: it has no
route to Postgres or to the internet.

## Files

| Path | Holds |
|---|---|
| `docker-compose.yml` | Postgres, the sidecar, dbhub 1.5.0 and Caddy, on four networks |
| `Caddyfile` | TLS, the `160.79.104.0/21` allowlist, `/mcp` only |
| `sidecar.yaml` | One postgres lane: a read-only rule and an email mask |
| `seed.sql` | A demo `customers` table and dbhub's login |
| `run.sh` | Starts the stack and writes `.env` with the token |
| `ant/` | An `ant apply` project: the environment, the vault, the agent |
| `credentials/` | The vault credential, without its secret |
| `docker-compose.test.yml`, `demo.sh`, `e2e.sh` | Tests: see [Try it](#try-it-without-a-public-host) |

## Before you start

- A Linux host with ports 80 and 443 open, and a DNS name for it. Below,
  `MCP_HOST` is that name. Docker Desktop is not supported: it hides the
  caller's address, so every call gets 403.
- Docker Compose 2.24 or later, `jq`, and `ant` 1.42.0 or later
  (`brew install anthropics/tap/ant`).
- `ANTHROPIC_API_KEY`: a key with Managed Agents access.

## 1. Run the stack

```bash
MCP_HOST=mcp.example.com ./run.sh
```

Caddy gets a Let's Encrypt certificate for `MCP_HOST`. `.env` holds the
token, `DBHUB_AUTH_TOKEN`; keep it out of git.

To use your own database:

- Remove the `postgres` service, and set `upstream` in `sidecar.yaml`.
- Put the sidecar, and only the sidecar, on a network that reaches the
  database. Across a network you do not trust, set `upstream_tls` on the
  lane (sidecar README, "Upstream TLS").
- Set dbhub's `DSN` to a login like the `agent` role in `seed.sql`: SELECT
  only, no TEMPORARY, no large-object creation, read-only by default, and a
  `statement_timeout`. Replace the demo passwords.

## 2. Create the environment, vault and agent

Replace `mcp.example.com` with `MCP_HOST` in `ant/agents/postgres-analyst.md`
and `credentials/dbhub-token.yaml`. Then:

```bash
cd ant && ant apply .    # writes claude-lock.json; commit it
lockid() { jq -r --arg f "$1" '.resources[$f].id' claude-lock.json; }
AGENT_ID=$(lockid ./agents/postgres-analyst.md)
ENV_ID=$(lockid ./environments/hoop-postgres-mcp.yaml)
VAULT_ID=$(lockid ./vaults/hoop-postgres-mcp.yaml)
cd ..
```

## 3. Add the credential

`ant apply` cannot hold a secret. `@<(...)` keeps the token out of the
process list.

```bash
ant beta:vaults:credentials create --vault-id "$VAULT_ID" \
  --auth.token @<(sed -n 's/^DBHUB_AUTH_TOKEN=//p' .env | tr -d '\n') \
  < credentials/dbhub-token.yaml
```

The vault sends it as `Authorization: Bearer <token>` to the URL it names.

## 4. Start a session

```bash
SESSION_ID=$(ant beta:sessions create --agent "$AGENT_ID" \
  --environment-id "$ENV_ID" --vault-id "$VAULT_ID" --transform id --raw-output)
ant beta:sessions:events send --session-id "$SESSION_ID" --event \
  "{type: user.message, content: [{type: text, text: 'Which plan is each customer on?'}]}"
ant beta:sessions connect --session-id "$SESSION_ID"
```

## 5. Read the audit trail

```bash
docker compose logs --no-log-prefix sidecar | grep '^{' \
  | jq -c 'select(.direction == "client") | {kind, operation, allowed, rule, statement}'
```

## What to expect

| The agent | Result |
|---|---|
| Runs a SELECT | Rows, with emails as `[REDACTED:EMAIL_ADDRESS]` |
| Runs a write, also in a CTE, `MERGE`, `DO`, `COPY FROM` or `EXPLAIN ANALYZE` | A tool error: `this connection is read-only; writes are not permitted`. Postgres never receives it. |
| Any statement | One audit row, direction `client`, principal `agent` |
| A caller outside `160.79.104.0/21` | `403 forbidden` from Caddy |
| No token, or a wrong one | `401` from dbhub |

## Try it without a public host

```bash
./run.sh --test && ./demo.sh    # every claim above, no Anthropic call
./run.sh --tunnel && ./e2e.sh   # a real session through a Cloudflare quick tunnel
./run.sh down
```

In both modes Caddy takes the caller's address from `Cf-Connecting-Ip`,
and trusts it only from the tunnel's own subnet. A quick tunnel is for
tests: it has no uptime guarantee and a new name on each run. `e2e.sh`
writes the transcript, audit rows and Caddy log to `evidence/`.

## Limits

- **A refusal closes the connection.** The sidecar sends `FATAL 42501` and
  closes it. dbhub's pool opens a new one, and the next statement works.
  ENG-800 tracks a refusal that keeps the connection open.
- **A refused write in a multi-statement call restarts dbhub.** dbhub 1.5.0
  exits on the closed connection (bytebase/dbhub#479). Docker restarts it.
  That call gets no answer, and other calls fail until dbhub is healthy.
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
- **No Claude session in the audit.** dbhub uses one login and one pool, so
  each row names `agent`, not a session or a person.
- **One token for every agent.** Every Anthropic customer's MCP calls come
  from the same range, so the range proves only that the call came from
  Anthropic. The token is what admits your agent.
- **Free tier.** One guardrail rule and one mask rule per process. A license
  lifts both caps.
- **Audit rows hold SQL text.** A literal in a statement is recorded as
  written. Masking applies to results.
- **The range is in `Caddyfile`.** If Anthropic changes it
  ([IP addresses](https://platform.claude.com/docs/en/api/ip-addresses)),
  update the file.
