# Testing the AI analyzer against Google Vertex AI

Four scripts, run in order. Each one is useful alone, and each fails loudly
rather than continuing on a broken assumption.

```bash
cd sidecar/scripts/dev

./00-preflight.sh          # GCP reachable? model enabled? IAM correct?
./01-validate.sh           # does the config load, and does the credential mint?
./02-run.sh                # upstreams + relay, in the background
./03-verify.sh             # 14 assertions against the running relay
./02-run.sh down           # tear down
```

Total runtime is about a minute after the first image pull. Steps 00 and 01
spend one Vertex call between them; step 03 spends four.

## What each one proves

| Script | Answers | Costs |
|---|---|---|
| `00-preflight.sh` | Is this a GCP problem or a hoop problem? | 1 Vertex call |
| `01-validate.sh` | Does the config parse, and can the relay mint a token? | 0 |
| `02-run.sh` | Postgres, httpbin and the relay are up | 0 |
| `03-verify.sh` | Blocking, caching, triggers, audit, both lanes | ~4 Vertex calls |

Run `00-preflight.sh` first even when you are sure. It issues the same
`rawPredict` request the relay does, so a failure there tells you the problem
is IAM, model enablement or a region name, before any hoop code is involved.

`01-validate.sh` cannot replace it. Validation mints a token and stops there,
on purpose: calling the model would bill you on every config check. A GCP
token is not project-scoped, so **a config naming a project you cannot reach
still validates clean**. Step 00 is the only one that proves the model
answers.

## Configuration

Every script reads the same three environment variables:

```bash
export HOOP_PROJECT=my-gcp-project        # required
export HOOP_REGION=global                 # default: global
export HOOP_MODEL=claude-sonnet-4-5@20250929
```

Credentials come from Application Default Credentials by default:

```bash
gcloud auth application-default login
gcloud auth application-default set-quota-project "$HOOP_PROJECT"
```

To test a service-account key file instead, point at it and the config picks
it up:

```bash
export HOOP_CREDENTIALS_FILE=$HOME/vertex-sa.json
chmod 600 "$HOOP_CREDENTIALS_FILE"        # 0644 is refused at startup
```

## Prerequisites

`gcloud`, `docker`, `go`, `curl`, `psql`, `python3`, and one IAM binding:

```bash
gcloud projects add-iam-policy-binding "$HOOP_PROJECT" \
  --member="user:$(gcloud config get-value account)" \
  --role="roles/aiplatform.user"
```

Claude on Vertex is a Model Garden partner model. Enable the specific model
once, in the console, or `rawPredict` returns 404:

<https://console.cloud.google.com/vertex-ai/publishers/anthropic/model-garden/claude-sonnet-4-5>

## Reading the results

`03-verify.sh` prints one line per assertion and exits non-zero if any fail.
The interesting ones:

- **`select costs no model call`** proves the trigger works. On a real
  database lane most traffic takes this path, and it is what makes the
  feature affordable.
- **`repeat shape served from cache`** proves the cache keys on statement
  shape rather than bytes. `WHERE id = 1` and `WHERE id = 2` are one verdict.
- **`http lane blocks a destructive payload`** only passes with
  `capture_body: true`. Without it the model sees `POST /anything` and no
  payload.
- **`audit records risk_level`** is what feeds `GET /api/sessions` and
  `/api/stats`, so a session's highest risk shows up in the rollup.

## Testing without spending anything

`04-offline.sh` runs the same assertions against a fake provider that speaks
the Anthropic wire format. It needs no GCP, no credential and no network, and
it exercises the identical code path, since Vertex differs from Anthropic only
in its URL and auth header.

```bash
./04-offline.sh
```

Use it in CI, or when you want to change the relay and re-test in two seconds.

## Two limits this suite exposed

**A slow classification can outlive the upstream's idle budget.** The relay
dials the upstream when it accepts a connection, then holds the request while
it classifies. gunicorn defaults to `keep_alive = 2s`, a Vertex call takes
three to five seconds, and the upstream hangs up first. The client sees an
empty reply rather than a verdict.

`02-run.sh` starts httpbin with `--keep-alive 75` for this reason. In a real
deployment, raise the upstream's idle timeout above your analyzer's p99, or
keep the analyzer off the paths that cannot afford the latency. The cache
hides this after the first call for any given statement shape, which makes it
a first-request problem and easy to miss in testing.

**A model can refuse to classify.** The reply carries `stop_reason: refusal`
and no content, so there is no verdict, and under `fail_open: true` the
statement is allowed. The relay reports it distinctly:

```
analyzer/vertex: model refused to classify this statement
```

`03-verify.sh` counts these and says so, because an enforcement check failing
for this reason is not a fault in the relay. Refusals are deterministic per
input on the models tested: the same statement refuses every time, and a
different phrasing of the same risk classifies fine.

Pick a model that reliably calls tools. Before trusting one, run its verdicts
past `00-preflight.sh`, which fails when tool calling does not work at all,
and then watch for refusals in the audit trail during an observe-only rollout.

## Failure modes and where to look

| Symptom | Cause |
|---|---|
| `00` fails with 403 | Missing `roles/aiplatform.user` |
| `00` fails with 404 on the model path | Model not enabled in Model Garden, or absent from this region |
| `00` fails with a DNS error | `HOOP_REGION=global` uses the unprefixed host. A typo'd region resolves nowhere |
| `01` fails minting a token | Stale ADC. Re-run `gcloud auth application-default login` |
| `01` passes but traffic is never classified | The project or model is wrong. Validation does not call the API; run `00` |
| Statements pass with `provider returned 403` in the audit trail | `fail_open: true` doing its job. Fix the IAM binding, or set `fail_open: false` |
| `curl: (52) Empty reply` on the first request of a shape | The upstream's keep-alive is shorter than the classification. Raise it |
| `model refused to classify this statement` | The model declined. It carries no verdict, so fail_open allowed the statement |
| `01` reports a mode | Credential file is group- or world-readable. `chmod 600` |
| `03` says the HTTP lane did not block | `capture_body` is off, or the request carried no body |
| Everything passes but nothing is classified | The trigger does not match. Compare the audit line's `operation` against the rule's `trigger` |

---

# Testing the MSSQL lane against SQL Server and Kerberos

Two scripts, independent of the Vertex ones above. They drive the compose
stack in `deploy/docker-compose/envoy-stack`, and none of it ships in an image.

```bash
cd sidecar/scripts/dev

./mssql-stack.sh            # mint certs, build, start, seed
./mssql-kerberos-check.sh   # verify: data path, guardrail, audit, Kerberos
./mssql-stack.sh down       # tear down, including volumes and certs
```

Also `./mssql-stack.sh --rebuild` (no-cache build), `./mssql-stack.sh logs
[service]`, and `./mssql-stack.sh psql` for the postgres lane.

No cloud account and no API spend, since it runs locally. Microsoft publishes
no arm64 image for SQL Server, so on Apple Silicon it runs emulated and first
boot takes a few minutes.

## The stack it brings up

```
sqlclient ──TLS (TDS 8.0)──> envoy ──plaintext TDS──> hoop-inspect ──> mssql
 kinit alice                terminates TLS           inspects, denies    keytab
                                                          │
                                                       samba (AD DC)
```

`mssql-stack.sh` **always rebuilds** the sidecar image. The base `run.sh`
reuses an existing one, which turns an edited codec into `unsupported protocol
"mssql"` at startup, a message pointing at the config while a stale image
causes it. Docker's layer cache keeps the rebuild cheap.

## The check runs each Kerberos login twice

`mssql.hoop.test` goes through Envoy and the relay. `sqlhost.hoop.test` reaches
SQL Server directly, past both. The check runs the same login down each path
and compares them:

| Outcome | Reported as | Meaning |
|---|---|---|
| Login succeeds through the relay | `PASS` | end to end |
| Both paths fail identically | `GAP` | the relay carried the ticket to a server that would have refused it anyway |
| The two paths **disagree** | `FAIL` | a divergence is our bug, and the one result implicating hoop-inspect |

Today it reports `GAP`, because SQL Server on Linux does not resolve
`HOOP\alice` against the directory (error 18452). The comparison has already
earned its keep: it caught a missing SPN in the domain controller's
provisioning that a one-sided test would have scored green.

See `deploy/docker-compose/envoy-stack/mssql/README.md` for the topology, the
protocol reasoning, and the list of what works and what does not.

---

# Testing the MSSQL lane against SQL Server 2019

A second, smaller stack for the TDS 7.4 case the 2022 one cannot reach.

```bash
cd sidecar/scripts/dev

./mssql2019-stack.sh        # build, start, seed
./mssql2019-check.sh        # verify: 13 checks
./mssql2019-stack.sh down   # tear down
```

Also `./mssql2019-stack.sh --rebuild`, `logs [service]`, and `sql` for a
sqlcmd session through the relay.

No certificates to mint. The 2022 lane needs a chain because `Encrypt=strict`
forbids `TrustServerCertificate`; here SQL Server runs with no TLS
configuration at all, which is the case under test. It mints a self-signed
certificate at startup and encrypts the login with it regardless.

## What it exists to prove

`ENCRYPT_OFF` means "encrypt the login only", so a 2019 server puts the first
LOGIN7 packet inside TLS and every statement in the clear. The relay meets a
raw TLS record where a TDS packet header should be, walks it by TLS record
framing, and resumes on the byte where plaintext returns.

Envoy is absent from this stack, unlike the 2022 one. TDS 7.x carries its
handshake inside `0x12` PRELOGIN packets, which Envoy cannot speak, so there
is nothing for it to terminate and the relay takes the connection directly.

Both negotiated outcomes come from one server:

```
sqlcmd -No -C   Encrypt=Optional  -> ENCRYPT_OFF -> inspected
sqlcmd -Nm -C   Encrypt=Mandatory -> ENCRYPT_ON  -> refused, rule stream-unsafe
```

See `deploy/docker-compose/envoy-stack/mssql2019/README.md` for a capture of
the handshake and the reasoning behind each check.
