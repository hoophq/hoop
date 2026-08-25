# Running `hoop start sidecar` from the agent image

The inspection relay ships **inside the images you already pull**. No separate
binary, no separate image, no extra registry credential. `hoop start sidecar`
is a subcommand of the same `hoop` binary that runs the agent. It is available
starting with the release containing this rename; older images use
`hoop start inspect`.

The command was named `hoop start inspect`, and `HOOP_SIDECAR_CONFIG` was
`HOOP_INSPECT_CONFIG`. Both names work in rename-capable images, so an image
or manifest written before the rename can continue using the old names. The
command prints a notice naming the new one.

| Image | Where it lives | Tag to pull |
|---|---|---|
| `hoophq/hoopdev` | agent (this is almost certainly yours) | `1.133.1` |
| `hoophq/hoop` | gateway | `1.133.1` |

Both are multiarch (`linux/amd64`, `linux/arm64`).

Pin the tag rather than `latest`, so a later pull cannot move the binary
underneath a test you are in the middle of.

```bash
docker pull hoophq/hoopdev:1.133.1
docker run --rm --entrypoint hoop hoophq/hoopdev:1.133.1 start inspect --help
```

Three ways to run it, cheapest first. All three use the same binary and the
same config file; they differ only in how the process gets started.

---

## Before anything: write a config

One file is the whole configuration. Nothing else is read. Minimal Postgres
lane, which denies destructive SQL and masks two fields on the way back:

```yaml
# config.yaml
log_level: info

admin:
  listen: 0.0.0.0:19000     # /healthz /stats /config /events /api/*

audit:
  file: "-"                 # stdout as JSON lines
  memory_buffer: 256
  query_sessions: 500       # required for GET /api/sessions; omit it and that route 404s

pii:
  entities: [EMAIL_ADDRESS, US_SSN]

policy:
  enforce: true             # false is observe-only: inspect and audit, deny nothing

mask:
  enabled: true

listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432   # what your client connects to
    upstream: appdb:5432    # the real database
    connection: appdb       # the name audit rows key on
    policy:
      rules:
        - name: no-destructive-sql
          type: operation
          operations: [drop, delete, truncate]
          message: destructive statements are not permitted on appdb
    mask:
      rules:
        - {name: ssn-column, columns: [ssn], strategy: partial, keep_last: 4}
        - {name: emails, entity: EMAIL_ADDRESS, strategy: redact}
```

Check it before you deploy it. This starts no listener and needs nothing
running:

```bash
docker run --rm -v "$PWD/config.yaml:/etc/hoop-inspect/config.yaml:ro" \
  --entrypoint hoop hoophq/hoopdev:1.133.1 \
  start inspect --config /etc/hoop-inspect/config.yaml --validate
```

```
config OK: 1 listener(s)
  appdb            postgres  enforcing 1 rule(s) + masking
```

That line is the **resolved** lane, so the rule count includes anything it
inherited from the top-level defaults. Validation builds every lane, which
catches what a syntax check cannot: a key typo, a bad regex, a `pii` rule
naming an entity that `pii.entities` never enabled.

Full config reference, every rule type and every masking strategy:
[`hoopinspect/README.md`](./README.md).

---

## Option 1 — exec into a running container (fastest, no rebuild)

Nothing to build and no restart, so the agent keeps its gateway connection.
Good for a first look; the relay dies with the container.

```bash
# 1. Put the config inside the container that is already running.
docker cp config.yaml <agent-container>:/tmp/inspect.yaml

# 2. Check it.
docker exec <agent-container> hoop start sidecar --config /tmp/inspect.yaml --validate

# 3. Start the relay as a second process.
docker exec -d <agent-container> \
  sh -c 'hoop start sidecar --config /tmp/inspect.yaml > /tmp/inspect.log 2>&1'

# 4. Confirm it is up.
docker exec <agent-container> curl -s localhost:19000/healthz     # ok
docker exec <agent-container> cat /tmp/inspect.log
```

On Kubernetes the same three steps, with `kubectl`:

```bash
kubectl cp config.yaml <pod>:/tmp/inspect.yaml -c agent
kubectl exec <pod> -c agent -- hoop start sidecar --config /tmp/inspect.yaml --validate
kubectl exec <pod> -c agent -- \
  sh -c 'nohup hoop start sidecar --config /tmp/inspect.yaml > /tmp/inspect.log 2>&1 &'
```

Both are ephemeral by construction: a pod restart, a rollout or an OOM kill
takes the relay with it and does not bring it back. Use option 2 or 3 for
anything that has to survive.

---

## Option 2 — extend the image, change `CMD`

The relay becomes the container's main process, so the supervisor restarts it
like anything else. This container runs the relay **instead of** the agent.

```dockerfile
# Dockerfile
ARG HOOP_TAG=1.133.1
FROM hoophq/hoopdev:${HOOP_TAG}

COPY config.yaml /etc/hoop-inspect/config.yaml
ENV HOOP_INSPECT_CONFIG=/etc/hoop-inspect/config.yaml

# Fail the build, not the pod, when the config is wrong.
RUN hoop start inspect --config /etc/hoop-inspect/config.yaml --validate

CMD ["hoop", "start", "inspect"]
```

```bash
docker build -t my-hoop-inspect:1.133.1 .
docker run -d --name inspect -p 15432:15432 -p 19000:19000 my-hoop-inspect:1.133.1
```

`--config` also reads `HOOP_SIDECAR_CONFIG`, which is why the `CMD` carries no
flags. Setting the variable is what a Kubernetes deployment wants anyway: mount
the ConfigMap, set the variable, pass no arguments.

The `RUN ... --validate` line is worth keeping. A typo becomes a red build on
your laptop instead of a CrashLoopBackOff at 3am.

Bake the config only while you are iterating locally. For anything shared,
mount it and drop the `COPY`, so a policy change is a ConfigMap edit rather
than a rebuild.

---

## Option 3 — a second container, same image (recommended for Kubernetes)

Keeps the agent doing its job and gives the relay its own lifecycle, restart
policy and resource limits. Same image, different `command`.

```yaml
# In the hoopagent Deployment, alongside the existing `agent` container.
containers:
  - name: agent
    image: hoophq/hoopdev:1.133.1
    # ... unchanged

  - name: hoop-inspect
    image: hoophq/hoopdev:1.133.1        # the very same image
    command: ["hoop", "start", "inspect"]
    env:
      - name: HOOP_INSPECT_CONFIG
        value: /etc/hoop-inspect/config.yaml
    ports:
      - {containerPort: 15432, name: pg}
      - {containerPort: 19000, name: admin}
    readinessProbe:
      httpGet: {path: /healthz, port: 19000}
    volumeMounts:
      - name: inspect-config
        mountPath: /etc/hoop-inspect
        readOnly: true

volumes:
  - name: inspect-config
    configMap:
      name: hoop-inspect-config          # kubectl create configmap hoop-inspect-config --from-file=config.yaml
```

Two containers in one pod share a network namespace, so **they cannot both bind
15432 or 19000**. That is fine here (only the relay binds them) and it is the
first thing to check if the container exits with `address already in use`.

Point your client at the relay's port instead of the database's, and change
nothing else.

---

## Prove it works

Against a lane configured like the one above:

```bash
# Masked on the way back.
PGPASSWORD=… PGSSLMODE=disable psql -h <relay-host> -p 15432 -U appuser -d appdb \
  -c 'SELECT name, email, ssn FROM customers;'
```

```
 name  |          email           |     ssn
-------+--------------------------+-------------
 Alice | [REDACTED:EMAIL_ADDRESS] | ***-**-4321
 Bob   | [REDACTED:EMAIL_ADDRESS] | ***-**-9876
```

```bash
# Refused, in a real pgwire error frame with your own message.
… psql … -c 'DELETE FROM customers WHERE id = 1;'
```

```
FATAL:  destructive statements are not permitted on appdb
```

The row is still there. The statement never reached the database.

**`PGSSLMODE=disable` is required on the CLIENT.** Nothing terminates
downstream TLS at the relay, so a client that negotiates TLS end-to-end leaves
no plaintext to parse and inspection is impossible. The hop from the relay to
the database is encrypted separately, and independently: see
[Upstream TLS](./README.md#upstream-tls).

### Read the evidence

```bash
curl -s localhost:19000/stats                  | python3 -m json.tool
curl -s 'localhost:19000/api/sessions?limit=5' | python3 -m json.tool
curl -s localhost:19000/config                 | python3 -m json.tool
```

```json
{"listeners": [{"name": "appdb", "addr": "[::]:15432",
                "active": 0, "total": 2, "denied": 1}],
 "version": "1.133.1"}
```

`/config` reports what each lane **resolved** to after inheritance, which is
where to start when a rule you wrote never fires. Every statement also lands
in the audit stream on stdout as one JSON line:

```json
{"level":"INFO","msg":"statement denied","listener":"appdb",
 "session":"da7b02e2…","rule":"no-destructive-sql",
 "message":"destructive statements are not permitted on appdb"}
```

---

## Turning on AI risk analysis

A `type: ai_analysis` rule sends a statement to a language model and denies on
the risk it reports. It works on both lane types: SQL on a postgres listener,
request bodies on an HTTP one. The relay holds the provider credential; there
is no gateway call.

Add an `analyzer:` block and a rule:

```yaml
analyzer:
  provider: vertex                 # vertex | anthropic | openai
  model: claude-sonnet-4-5@20250929
  extra: {project: my-gcp-project, region: global}
  # credentials_file omitted -> Application Default Credentials.
  # On GKE with Workload Identity there is then no credential on disk at all.
  timeout_sec: 10
  fail_open: true                  # the default; see below
  send: redacted                   # raw | redacted | refuse
  cache: {size: 4096, ttl_sec: 900}
  max_calls: 500

listeners:
  - name: appdb
    # ... as above
    policy:
      rules:
        - name: risky-writes
          type: ai_analysis
          trigger: {operations: [update, delete]}
          high: block
          medium: warn
          low: allow
          message: refused by risk analysis
```

`--validate` reports it, and for Vertex it mints one token so a bad key or a
missing `roles/aiplatform.user` binding fails here rather than on the first
risky statement:

```
config OK: 1 listener(s)
  appdb            postgres  enforcing 1 rule(s) + masking + 1 ai rule(s)
```

### Three knobs decide what it costs

An ORM issues the same statement shape thousands of times per session, so
these are not optimizations you can skip:

| Knob | Effect |
|---|---|
| `trigger` | Only these operations, tables or resources are classified. Everything else is free. An empty trigger classifies nothing and is a startup error. |
| `cache` | Keys on the statement SHAPE. `WHERE id = 1` and `WHERE id = 2` are one verdict. |
| `max_calls` | Process-lifetime budget. Past it, statements fall through to the local rules. |

Read the hit rate before you enable `block` anywhere:

```bash
curl -s localhost:19000/stats | python3 -m json.tool
```

Roll out with every tier on `warn`, watch `by_risk` in `/api/stats` for a
week, then move `high` to `block`.

### For an HTTP lane, turn on body capture

The HTTP codec exposes nothing by default. Without a body the model sees `POST
/orders` and nothing else:

```yaml
  - name: api
    protocol: http
    listen: 0.0.0.0:18080
    upstream: internal-api:8080
    http:
      capture_body: true
      max_body_bytes: 8192
      headers: [Content-Type]      # authorization is refused at startup
    policy:
      rules:
        - name: risky-payloads
          type: ai_analysis
          trigger: {resources: ["/orders/**"]}
          high: block
```

A request with no body is skipped rather than classified, so a forgotten
`capture_body` looks like a rule that never fires.

### Reading the verdicts

Every classified statement carries its risk into the audit trail, allowed ones
included, and the store keeps the highest risk a session reached:

```bash
curl -s 'localhost:19000/api/sessions?limit=5' | python3 -m json.tool
curl -s localhost:19000/api/stats              | python3 -m json.tool
```

```json
{"id": "45edf2c8…", "connection": "appdb", "verdict": "clean",
 "risk_level": "medium"}
```

A blocked statement reaches the developer the same way any other denial does,
carrying the model's own one-line title:

```
FATAL:  unbounded delete against the customer ledger
```

### The credential

The config holds a **path**, never the key. There is no environment-variable
option and no `${VAR}` interpolation, on purpose: `/proc/<pid>/environ`,
`docker inspect` and a core dump all expose a process's environment, while a
`0400` file exposes it to none of them.

**On GKE, GCE or Cloud Run, use no credential at all.** Omit
`credentials_file` and Vertex resolves Application Default Credentials, so the
pod's identity is the credential and nothing sits on disk to leak or rotate:

```bash
gcloud iam service-accounts add-iam-policy-binding \
  hoop-inspect@$PROJECT.iam.gserviceaccount.com \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:$PROJECT.svc.id.goog[default/hoop-inspect]"
```

**Anywhere else, mount a file.** Keep it in its own volume so the ConfigMap
stays safe to read and diff:

```yaml
  - name: hoop-inspect
    volumeMounts:
      - {name: inspect-config, mountPath: /etc/hoop-inspect,  readOnly: true}
      - {name: llm-key,        mountPath: /run/secrets/vertex, readOnly: true}
volumes:
  - name: llm-key
    secret:
      secretName: hoop-inspect-llm
      defaultMode: 0400        # required, see below
```

`defaultMode: 0400` is not optional. Kubernetes mounts secret files `0644` by
default and the relay refuses that, naming the mode:

```
credential file is readable by group or other:
/run/secrets/vertex/key.json is 0644, want 0600 or stricter
```

Writing the file by hand, use `printf` rather than `echo`, which appends a
newline:

```bash
printf '%s' "$KEY" > /run/secrets/anthropic-key && chmod 600 $_
```

Once loaded, the key is held in a type that renders `[REDACTED]` through
`%v`, JSON marshalling and structured logs alike, and `/config` reports the
endpoint host and whether a custom prompt is set, never the credential.

### The fail-open default

This evaluator defaults to `fail_open: true`, while the local rules and OPA
fail closed. It depends on a third-party API rather than a service you run,
and refusing every `UPDATE` during a vendor outage turns "we could not score
this statement" into "the database is down".

The local rules and OPA still ran and still allowed, so a silent analyzer
means "guarded by everything except the optional expensive layer". The error
still reaches the audit trail. Set `fail_open: false` where the classification
is a compliance requirement.

### Writing your own prompt

`analyzer.prompt` sets the risk guidance for every rule; a rule's own
`prompt:` beats it.

`analyzer.prompt` reaches **every lane**, so keep it protocol-neutral and put
SQL- or HTTP-specific wording on the rule. Guidance reading "you are
classifying SQL against a customer database" follows an HTTP statement to the
model and has it reasoning about `DROP` while it looks at a JSON body.

Either way, two instructions are appended and cannot be removed: call exactly
one risk tool, and never quote a literal value from the statement. The second
is why a verdict never repeats the identifier it objected to into your audit
log.

---

## Three things that cost people time

**`enforce` defaults to `false`.** A lane with rules and no `enforce: true`
inspects and audits and denies nothing. It says `observe-only` in the startup
log and in `/config`. That default is deliberate — a misconfigured rule should
not take production down on first deploy — but it surprises everyone once.

**A `pii` rule naming an entity absent from `pii.entities` is refused at
startup.** Without that check the rule would load, evaluate and match nothing,
so a guardrail would look live while allowing everything it was written to
stop.

**Masking column rules beat detection.** `{columns: [ssn]}` masks whatever is
in that column, whatever it looks like. Entity rules depend on the detector
recognizing the value, and the detector deliberately refuses obvious fixtures
like `123-45-6789`. Use a column rule when the protocol names the value, an
entity rule when it does not.

**An `ai_analysis` rule on an HTTP lane needs `capture_body: true`.** The HTTP
codec exposes no body by default, and a request with no body is skipped rather
than classified, so the symptom is a rule that does not fire rather than an
error. The same rule on a postgres lane needs nothing extra, because the
statement text is there either way.

---

## Where to go next

- [`hoopinspect/README.md`](./README.md) — full config reference, every rule
  type, masking strategies, unix sockets, upstream TLS, OPA.
- [`deploy/docker-compose/envoy-stack`](../deploy/docker-compose/envoy-stack) —
  a runnable stack: Envoy terminating TLS, OPA answering reachability, the
  relay behind it, Postgres and an HTTP service behind that. `./run.sh` then
  `./demo.sh`.
- [`docs/adr/hoopinspect-flow.md`](../docs/adr/hoopinspect-flow.md) — the code
  path behind each command, a per-command runbook and a troubleshooting table.
  [Risk analysis](../docs/adr/hoopinspect-flow.md#risk-analysis-the-ai-session-analyzer)
  covers the fail-open default, the cache key, and the half of the prompt a
  config cannot replace.
