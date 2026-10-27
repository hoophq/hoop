# Resources seed

Writes a resource per type family of the Resources behavior matrix (project
"Resources, Onboarding and AWS Connect in React"), the roles the matrix needs,
and three AWS Connect jobs. Both UIs, CLJS and React, then show the same rows.
Two runs write the same rows.

## Run

```sh
make seed-dev-resources         # the seed
make seed-dev-resources-empty   # no connection, no resource, no protection profile
```

It needs Node 20+, `psql`, and a `make run-dev` stack with `AUTH_METHOD=local`.
Any license works. No Go. A second run deletes the seeded rows, and the roles
a flow added to a seeded resource, and writes them again.

The variables of the sessions seed apply (`SEED_DB_URI`, `API_URL`, `PSQL`,
`SEED_ORG`, `ADMIN_USERNAME`; see `../seed-sessions/README.md`), plus:

| Variable | Default | |
|---|---|---|
| `SEED_AGENT` | `default` | The agent every role runs on. `make run-dev` starts `default`. |

What it does:

1. Upserts the two users below, logs in as the admin, stops when the gateway
   at `API_URL` does not serve the organization of `SEED_DB_URI`, and turns
   `experimental.agents` on: the menu shows these pages only with it.
2. Writes, by SQL, the resources, roles, tags, attributes, the BigQuery
   federation config, the sidecar mirror and the jobs below.
3. Sends the federation's admin credentials with
   `PUT /api/connections/:role/federation`: the gateway encrypts them with its
   own key.
4. Reads every row back through the routes the pages call, and stops when one
   differs.

`empty` deletes every connection and resource of the organization, and sets
the protection profile to none through `PUT /api/orgs/protection-profile`. An
event subscription that holds a connection stops it; nothing is deleted then.

A role is online when the agent is connected at seed time. Times are fixed.

## Users

Password for both: `hoop-seed-1234`.

| Role | Email | Groups |
|---|---|---|
| admin | `admin@seed.example.com` | `admin` |
| standard | `standard@seed.example.com` | none |

The admin is the sessions seed's admin.

## Resources and roles

Roles take the resource's type and subtype, store their secrets as the wizard
sends them (base64, `envvar:` and `filesystem:` keys), and have every access
mode enabled. The hosts do not exist.

| Resource | Type / subtype | Roles | What |
|---|---|---|---|
| `seed-res-postgres` | database / postgres | `seed-res-postgres-ro`, `seed-res-postgres-rw`, `seed-res-postgres-iam` | Metadata-driven database. `-ro`: tags `env=prod`, `team=data`, attributes `seed-pii`, `seed-prod`. `-rw`: tag `env=staging`, attribute `seed-prod`. `-iam`: AWS IAM method (`_aws_iam_rds:`). |
| `seed-res-python` | custom / python | `seed-res-python` | Dev env: no secret, command `python3 -`. |
| `seed-res-ssh` | application / ssh | `seed-res-ssh-password`, `seed-res-ssh-key` | SSH by password, and by key (`AUTHORIZED_SERVER_KEYS`). |
| `seed-res-tcp` | application / tcp | `seed-res-tcp` | TCP. |
| `seed-res-linux-vm` | custom / linux-vm | `seed-res-linux-vm` | Linux VM, command `[]` as the wizard writes it (Q17). |
| `seed-res-http` | httpproxy / httpproxy | `seed-res-http` | HTTP proxy, header `HEADER_Authorization`. |
| `seed-res-claude-code` | httpproxy / claude-code | `seed-res-claude-code` | Claude Code, Anthropic provider. |
| `seed-res-mcp` | httpproxy / mcp | `seed-res-mcp` | MCP, the token an OAuth login stores. |
| `seed-res-mcp-gateway` | httpproxy / mcpproxy | `seed-res-mcp-gateway` | MCP Gateway, remote, static token. |
| `seed-res-bigquery` | custom / bigquery | `seed-res-bigquery-federated`, `seed-res-bigquery-static` | BigQuery. `-federated` has a `gcp_iam` federation config with admin credentials. |
| `seed-res-many` | database / postgres | `seed-res-many-01` to `seed-res-many-55` | More than a page of 50 roles. |
| `seed-res-no-roles` | database / mysql | none | A resource without roles. |
| `seed-agent-managed` | custom / (none) | `seed-agent-managed` | `managed_by=hoopagent`, as an agent syncs it. |
| `seed-sidecar-appdb` | database / postgres | `seed-sidecar-appdb` | A sidecar mirror of listener `appdb` of `seed-sidecar`. No list shows it. |

Attributes: `seed-prod`, `seed-pii`. Seeded alone, `/resources` lists 13
resources and 70 roles.

## AWS Connect jobs

| Key | Database | Status |
|---|---|---|
| `running` | `billing-staging` | running, no result |
| `completed` | `orders-prod` | completed, 3 roles, a hook output |
| `failed` | `analytics-dev` | failed, `hoop_rw` failed with a message |

## Check the rows

```sh
SEED_DB_URI=... node scripts/dev/seed-resources/seed.mjs dump > a.dump
```

`dump` prints the tables the seed writes, one row per line, without `org_id`.
An agent shows as its name, a federation's admin credentials as
`has_admin_credentials`, a user without `last_login_at`. Tags and attributes
are the seed's only: the gateway writes its own at boot, with random ids. Seed
two new databases, dump both, and `diff` them: it is empty.

`make test-seed-resources` checks that the SQL does not read the clock, that
the catalog holds what ENG-909 lists, and that this README maps every SEED row
of the matrix to names the catalog has; CI runs it in the "Webapp v2 Lint and
Build" job. With `SEED_DB_URI` and `API_URL` set, it also seeds that stack
twice and compares the dumps.

## Matrix rows marked SEED

As admin unless the row names a user. A role stands for its row in
`/resources` (Roles tab); a resource for `/resources/configure/<resource>`.

| Row | Shown by |
|---|---|
| RL01 | `/resources`, `/resources?tab=resources` |
| RL02 | `/resources`: pick type postgres, then switch tabs |
| RL03 | admin and `standard@seed.example.com` on `/resources` |
| RL04 | Search `po`, then `pos`: `seed-res-postgres` |
| RL05 | Tags `env=prod` and `team=data`: `seed-res-postgres-ro` |
| RL06 | Attribute `seed-prod`: `seed-res-postgres-ro`, `seed-res-postgres-rw` |
| RL07 | Type postgres: `seed-res-postgres`, `seed-res-many`. mysql: `seed-res-no-roles`. ssh: `seed-res-ssh`. tcp: `seed-res-tcp`. mongodb: none |
| RL08 | Search `seed` alone, then type postgres |
| RL09 | 70 roles: 50, then 20 on scroll |
| RL10 | Type mongodb |
| RL11 | admin and `standard@seed.example.com`, Resources tab |
| RL12 | `seed-res-postgres-ro` (online) |
| RL13 | `seed-sidecar-appdb` is in no list |
| RL14 | Every role listed |
| RL15 | `seed-res-postgres-ro`, `seed-res-python`, `seed-res-linux-vm` (shown); `seed-res-ssh-password`, `seed-res-tcp`, `seed-res-http` (hidden) |
| RL16 | `seed-res-tcp`, `seed-res-python` |
| RL17 | `seed-res-ssh-password`, `seed-res-http`, `seed-res-claude-code`, `seed-res-mcp`, `seed-res-mcp-gateway`; `seed-res-postgres-ro` once the Postgres proxy is on |
| RL19 | `seed-agent-managed` (no Configure), `seed-res-postgres-ro` (Configure) |
| RL20 | `seed-res-many-55` |
| RL21 | `standard@seed.example.com` on `seed-res-postgres-ro` |
| RL22 | `/resources` |
| RL23 | `/resources`, throttled network |
| RL24 | Search `many`, type postgres, scroll |
| RL25 | Search `postgres`, then `ssh`, throttled network |
| RC01 | `standard@seed.example.com` on `seed-res-postgres` |
| RC02 | `seed-res-postgres` |
| RC03 | `/resources/configure/seed-res-postgres?tab=roles` |
| RC04 | `seed-res-postgres` |
| RC05 | `seed-res-no-roles`: rename it |
| RC06 | `seed-res-no-roles` (deleted), `seed-res-postgres` (403) |
| RC07 | `seed-res-postgres`, `seed-agent-managed` |
| RC08 | `seed-res-many` |
| RC09 | `seed-res-no-roles` |
| RC10 | `/resources/configure/seed-res-missing` |
| RA01 | `standard@seed.example.com` on `/resources/seed-res-postgres/add-role` |
| RA02 | `/resources/seed-res-postgres/add-role` |
| RA03 | `/resources/seed-res-postgres/add-role` |
| RA04 | `/resources/seed-res-postgres/add-role` |
| RA05 | `/resources/seed-res-postgres/add-role` |
| RA06 | Reload `/resources/seed-res-postgres/add-role` |
| RA07 | `/resources/seed-res-bigquery/add-role` |
| RA08 | `/resources/seed-res-postgres/add-role` with name `seed-res-postgres-ro` |
| RA09 | `/resources/seed-res-missing/add-role` |
| RW01 | `/resource-catalog`, PostgreSQL, Continue Setup |
| RW02 | Name `seed-res-postgres` |
| RW03 | Agent `default` |
| RW04 | Create an agent |
| RW05 | Roles step |
| RW06 | Attributes `seed-prod`, `seed-pii` |
| RW07 | PostgreSQL (AWS IAM), BigQuery (IAM Federation) |
| RW08 | Roles step, an empty required field |
| RW09 | Any family, compared with its seeded role (RF rows) |
| RW11 | BigQuery, IAM Federation, template `{user.handle}` |
| RW12 | Success step |
| RW13 | Success step, one and two roles |
| RW14 | Success step |
| RW15 | Success step |
| RW16 | Success step |
| RW18 | Add a role to `seed-res-postgres`, then create a resource |
| RW19 | Reload `/resources/new` |
| RF01 | `seed-res-postgres-ro`, `seed-res-postgres-iam` |
| RF02 | `seed-res-python` |
| RF03 | `seed-res-ssh-password`, `seed-res-ssh-key` |
| RF04 | `seed-res-tcp` |
| RF05 | `seed-res-linux-vm` |
| RF06 | `seed-res-http` |
| RF07 | `seed-res-claude-code` |
| RF09 | `seed-res-mcp-gateway` |
| RF10 | `seed-res-bigquery-federated`, `seed-res-bigquery-static` |
| CA01 | `/resource-catalog`, with and without `experimental.mcp_gateway` |
| CA02 | `/resource-catalog` |
| CA03 | `/resource-catalog` |
| CA04 | admin and `standard@seed.example.com` on a card |
| CA05 | PostgreSQL, HTTP Proxy, Kubernetes Token cards |
| ON01 | admin and `standard@seed.example.com` on `/onboarding` |
| AW01 | `standard@seed.example.com` on `/integrations/aws-connect` |
| AW02 | `running`, `completed`, `failed` |
| AW03 | No license upload (OSS): `running`, `completed` are not failed |
| AW04 | `/integrations/aws-connect/setup` |
| AW12 | `/integrations/aws-connect/setup` |
| LK01 | The sidebar |
| LK03 | `/roles/seed-res-postgres-ro/configure` |
| LK04 | Data Masking, a rule on `seed-res-postgres-ro` |
| LK06 | The CLJS sidebar |
| LK07 | `/resources` |
| Q1 | RL23 |
| Q2 | RL24 |
| Q3 | RL25 |
| Q4 | RL20, `seed-res-many-55` |
| Q5 | `seed-res-postgres` |
| Q6 | `seed-res-no-roles` |
| Q7 | `seed-res-no-roles` with a role added, renamed |
| Q8 | `/resources/configure/seed-res-missing` |
| Q9 | `seed-agent-managed` |
| Q10 | `seed-res-postgres` |
| Q11 | Reload `/resources/seed-res-postgres/add-role` |
| Q12 | `/resources/seed-res-bigquery/add-role` |
| Q13 | RA08 |
| Q14 | RW18 |
| Q15 | RW04, twice |
| Q16 | RW19 |
| Q17 | `seed-res-linux-vm` |
| Q18 | `seed-res-ssh-password`, `seed-res-tcp` |
| Q19 | Success step with `seed-res-ssh-password` and `seed-res-ssh-key` |
| Q20 | BigQuery with two federation roles |
| Q21 | Roles step, then rename the resource |
| Q22 | HTTP Proxy card, compared with `seed-res-http` |
| Q23 | Kubernetes Token card |
| Q28 | `running` |
| Q33 | AW12 |
| Q34 | `running`, `completed` |

## Matrix rows marked EMPTY

`make seed-dev-resources-empty`, as admin: RL03, RL10, RW17, CA06, ON02 to
ON12, AW10, LK02, LK05, Q24 to Q27, Q29. ON05 also needs no agent row:
`DELETE FROM private.agents WHERE name = 'default'`; `make run-dev` writes it
again at start.
