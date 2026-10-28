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

Needs [uv](https://docs.astral.sh/uv/) and agents-cli 1.11 or later
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
   agent runs (agents-cli's default is `us-east1`), and let the Agent Platform
   service agent use it:

   ```bash
   gcloud compute networks subnets create agent-psci --network NETWORK \
     --region REGION --range 192.168.10.0/28
   gcloud compute network-attachments create agent-psci --region REGION \
     --subnets agent-psci --connection-preference ACCEPT_AUTOMATIC
   gcloud iam roles create agentRuntimePsci --project PROJECT \
     --permissions compute.networkAttachments.get,compute.networkAttachments.update,compute.regionOperations.get
   gcloud projects add-iam-policy-binding PROJECT \
     --member serviceAccount:service-PROJECT_NUMBER@gcp-sa-aiplatform.iam.gserviceaccount.com \
     --role projects/PROJECT/roles/agentRuntimePsci
   ```

   The subnet is a free `/28`. Keep the `192.168.10.0/28` range if it is free:
   with `10.10.0.0/28`, Agent Runtime failed to start the agent and logged no
   cause. The custom role holds the three permissions Google documents for the
   service agent; `roles/compute.networkAdmin` also works. The attachment's
   subnet must reach the sidecar's IP on port 15432. It must not reach the
   database: the lock in step 1 sees to that.
3. **Deploy the agent**, with the database password in Secret Manager. The
   agent runs as its own Agent Identity, which exists only once the deploy
   starts, so grant the secret to every agent of the project first:

   ```bash
   printf %s "$APPDB_PASSWORD" | gcloud secrets create appdb-password --data-file=-
   gcloud secrets add-iam-policy-binding appdb-password \
     --role roles/secretmanager.secretAccessor \
     --member principalSet://agents.global.org-ORGANIZATION_ID.system.id.goog/attribute.platformContainer/aiplatform/projects/PROJECT_NUMBER
   agents-cli deploy \
     --network-attachment projects/PROJECT/regions/REGION/networkAttachments/agent-psci \
     --env-vars APPDB_HOST=SIDECAR_IP,APPDB_USER=agent \
     --secrets APPDB_PASSWORD=appdb-password
   ```

   A project with no organization uses `agents.global.proj-PROJECT_NUMBER.system.id.goog`
   instead. With a private DNS name for the sidecar instead of its IP, add
   `--dns-peering-domain`, `--dns-peering-project` and `--dns-peering-network`.
4. **Ask it**:

   ```bash
   agents-cli run "Which customers are on the enterprise plan?" \
     --url https://REGION-aiplatform.googleapis.com/v1/projects/PROJECT_NUMBER/locations/REGION/reasoningEngines/AGENT_ID \
     --mode adk
   ```

   A `DELETE` comes back as `hoop: this agent may read appdb, not change it`,
   and the sidecar's log holds the violation with the ADK session id
   (`postgres.option.adk.session.id`).

PSC-I does not stop the agent from calling Google front ends (`*.run.app`). A
VPC Service Controls perimeter does.

## Free tier

The sidecar is free. Without a license it enforces one guardrail rule and one
data masking rule per process. `deploy/sidecar/sidecar.yaml` uses the
guardrail rule: `agents-read-only`, which names eleven operations. One rule
can carry more than one check.

A config over the cap does not start. After `terraform apply`, the new
sidecar pod logs:

```
Error: invalid config:
  - 2 guardrail rules are configured (guardrails: 2) and this process enforces at most 1; merge them into one rule. A license lifts this cap: add one with the license flag, the HOOP_LICENSE environment variable, or the "license" key in the config file. Contact our support at https://help.hoop.dev. ai_analysis rules are counted separately and are not limited
```

The pod it replaces keeps serving with the old rules. `terraform apply` waits
for the new pod up to the module's Helm timeout (10 minutes), then fails: fix
`sidecar.yaml` and apply again. Check a config before you apply it; the output
names the limits:

```bash
docker run --rm -v "$PWD/deploy/sidecar:/c:ro" -e HOOP_SIDECAR_CONFIG=/c/sidecar.yaml \
  hoophq/hoopsidecar:1.281.0-distroless hoop start sidecar --validate
```

Use the image tag of `sidecar_version` in `deploy/sidecar/main.tf`.

A license lifts both caps: [talk to us](https://hoop.dev/meet). On a hoop
control plane, the sidecar takes the organization's license from the plane:
[`deploy/sidecar/README.md`](deploy/sidecar/README.md), "Later: connect it to
a hoop control plane".

## The agent

| Variable | Default | Holds |
|---|---|---|
| `APPDB_HOST` | required | The sidecar's internal IP or DNS name. Never the database |
| `APPDB_PORT` | `15432` | The sidecar's listener port |
| `APPDB_NAME` | `appdb` | The database |
| `APPDB_USER` | `agent` | The database login, with read grants only. The sidecar passes it through |
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
its root agent is not the one in `agents-cli-manifest.yaml`, or when the agent
does not start with no network (a PSC-I agent has no route to pypi.org). It
needs Docker. The address is the upstream's `host:port`, and also its host
when the host is an FQDN or an IPv4 address: a one-label host such as
`postgres` is an ordinary word too, so for it only `host:port` counts. An
IPv6 host is searched only in the project's own files, since the base image's
Python files hold the common IPv6 addresses.

## Files

| Path | Holds |
|---|---|
| `app/agent.py` | The agent and its `run_sql` tool |
| `Dockerfile` | agents-cli's, plus `UV_NO_SYNC=1`: the agent starts with no network |
| `deploy/sidecar/` | The sidecar: Terraform root, its rules, the database lock, its README |
| `tests/agent_image.sh` | The image test |

agents-cli adds the rest: `agents-cli install`, `agents-cli playground` and
`agents-cli deploy` work as in any agents-cli project.
