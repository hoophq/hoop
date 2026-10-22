# ADK agent with a hoop sidecar, on Agent Runtime

An ADK agent whose SQL tool reaches Postgres only through a hoop sidecar. The
sidecar runs in your VPC, behind an internal load balancer. On Agent Runtime
the agent reaches it over PSC-I and has no route to the database. The sidecar
refuses what its rules deny, and logs every statement with the ADK session
that sent it.

```
Agent Runtime                      your VPC
+--------------------+   PSC-I    +----------------------+        +----------+
| support_agent      |----------->| hoop sidecar         |------->| Postgres |
| APPDB_HOST=<ILB>   |            | internal LB, :15432  |        +----------+
+--------------------+            | rules, audit log     |   NetworkPolicy:
                                  +----------------------+   only the sidecar
```

By default the sidecar is free: its rules come from `deploy/sidecar/sidecar.yaml`,
with no license and no control plane. Two variables connect it to a hoop
control plane later, with no change to the agent.

## Create the project

Needs [uv](https://docs.astral.sh/uv/) and agents-cli
(`uv tool install google-agents-cli`).

```bash
agents-cli create my-agent -a hoophq/hoop/deploy/gcp-agent-runtime/postgres@<hoop release> \
  -d agent_runtime --root-agent-name support_agent
```

- `--root-agent-name support_agent` is required. agents-cli copies `app/agent.py`
  as is, and the agent in it is `support_agent`.
- `@<hoop release>` is a tag from the hoop releases. Without it, agents-cli takes
  `main`.
- Without uv, agents-cli writes a `pyproject.toml` with no `google-adk` and still
  reports success.

## Deploy

1. **Install the sidecar and lock the database to it**:
   [`deploy/sidecar/README.md`](deploy/sidecar/README.md). Note the sidecar's
   internal IP.
2. **Create a network attachment** in the sidecar's VPC, in the region where the
   agent runs (agents-cli's default is `us-east1`):

   ```bash
   gcloud compute networks subnets create agent-psci --network NETWORK \
     --region REGION --range 10.200.0.0/28
   gcloud compute network-attachments create agent-psci --region REGION \
     --subnets agent-psci --connection-preference ACCEPT_AUTOMATIC
   ```

   The attachment's subnet must reach the sidecar's IP on port 15432. It must
   not reach the database: the lock in step 1 sees to that.
3. **Deploy the agent**, with the database password in Secret Manager:

   ```bash
   printf %s "$APPDB_PASSWORD" | gcloud secrets create appdb-password --data-file=-
   agents-cli deploy \
     --network-attachment projects/PROJECT/regions/REGION/networkAttachments/agent-psci \
     --env-vars APPDB_HOST=SIDECAR_IP,APPDB_USER=agent \
     --secrets APPDB_PASSWORD=appdb-password
   ```

   With a private DNS name for the sidecar instead of its IP, add
   `--dns-peering-domain`, `--dns-peering-project` and `--dns-peering-network`.

PSC-I does not stop the agent from calling Google front ends (`*.run.app`). A
VPC Service Controls perimeter does.

## The agent

| Variable | Default | Holds |
|---|---|---|
| `APPDB_HOST` | required | The sidecar's internal IP or DNS name. Never the database |
| `APPDB_PORT` | `15432` | The sidecar's listener port |
| `APPDB_NAME` | `appdb` | The database |
| `APPDB_USER` | `agent` | The database login. The sidecar passes it through |
| `APPDB_PASSWORD` | empty | Its password. Give it with `--secrets` |
| `AGENT_MODEL` | `gemini-3.8-flash` | The Gemini model |

A denied statement comes back to the agent as a Postgres error, for example
`42501 hoop: this agent may read appdb, not change it`. The tool uses psycopg 3
on purpose: the sidecar sends the denial and closes the connection, and
psycopg2 and pg8000 report only the closed connection.

## Test

`tests/agent_image.sh` builds the image that `agents-cli deploy` builds. It
fails when the image holds the sidecar, its config or the database address
from `deploy/sidecar/sidecar.yaml`, when the image sets `APPDB_HOST`, or when
its root agent is not the one in `agents-cli-manifest.yaml`. It needs Docker.

## Files

| Path | Holds |
|---|---|
| `app/agent.py` | The agent and its `run_sql` tool |
| `deploy/sidecar/` | The sidecar: Terraform root, its rules, the database lock, its README |
| `tests/agent_image.sh` | The image test |

agents-cli adds the rest: `agents-cli install`, `agents-cli playground` and
`agents-cli deploy` work as in any agents-cli project.
