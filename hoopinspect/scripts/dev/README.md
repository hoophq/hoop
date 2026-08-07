# Testing the AI analyzer against Google Vertex AI

Four scripts, run in order. Each one is useful alone, and each fails loudly
rather than continuing on a broken assumption.

```bash
cd hoopinspect/scripts/dev

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
