# OpenAI Agents API: a REST API through the sidecar

An agent in an OpenAI Agents API hosted sandbox reaches one REST API, and
only through a hoop sidecar. The sidecar admits a caller by token, denies
writes, masks personal data in responses and records each request with the
OpenAI session id. `deploy/managed-agents/http` is the Claude twin.

```
hosted sandbox, restricted        your network
+----------------------------+   HTTPS    +--------------------+  HTTPS  +----------+
| agent: curl $SIDECAR_HOST  |--egress--->| hoop sidecar       |-------->| REST API |
| env: vault placeholders    |  proxy     | token, deny, mask, |         +----------+
+----------------------------+  fills the | audit              |
  any other host: refused       secrets   +--------------------+
```

The agent cannot skip the sidecar:

- The environment template allows one host, the sidecar's. The egress proxy
  refuses every other host, the API's included, and a session cannot widen
  the template's network.
- The sandbox holds vault placeholders. The egress proxy writes the real
  tokens into the request, and only toward the sidecar's host.
- The sidecar refuses a request without the caller token before any byte of
  it reaches the API.

Start with the [quick start](#quick-start): your laptop, hoop's demo API, no
cluster. Then [run it against your API](#run-it-against-your-api).

## Files

| Path | Holds |
|---|---|
| `quickstart/` | The quick start: a Compose stack, its sidecar config, `run.sh` and `agent.sh`, the demo agent in `openai/` |
| `values.yaml` | Sidecar chart values: one HTTPS lane on 443, the caller token, a read-only guardrail, a PII mask rule, `analytics.installSource` |
| `setup.sh` | Creates and deletes the OpenAI side from `openai/`, and starts a session. `quickstart/agent.sh` runs it too |
| `openai/` | The request bodies: the environment template, the vault, the two credentials without their secrets, the agent (`agent.json`, instructions in `agent.md`), the session |

## Quick start

The sidecar fronts hoop's demo API, a set of invented users with emails,
phones and cards. A Cloudflare quick tunnel gives it a public HTTPS host.
The tunnel is for a trial: it has no uptime promise and gets a new host on
each start.

You need Docker, `curl` 7.76 or later, `openssl`, `jq` 1.6 or later, and an
OpenAI API key with Agents API access in `OPENAI_API_KEY`
(`read -rs OPENAI_API_KEY && export OPENAI_API_KEY`).

From a clean clone with no image cached, `run.sh` took 23 to 100 s and
`agent.sh` 35 to 41 s to the agent's answer, over the cold runs of
2026-10-23. `run.sh` waits up to 2 minutes for a new tunnel host to resolve.

### 1. Start the sidecar

```bash
cd quickstart && ./run.sh
```

It prints the tunnel host and checks the lane through it:

```
  ok  GET /users/1 is masked:
        {
          "id": 1,
          "name": "Grace Hopper",
          "email": "[REDACTED:EMAIL_ADDRESS]",
          "phone": "[REDACTED:PHONE_NUMBER]",
          "card": "[REDACTED:CREDIT_CARD]",
          "plan": "enterprise"
        }
  ok  DELETE /users/1 is refused: this agent may only read from the API
  ok  a request without X-Hoop-Token is refused: request refused: the request carried no credential
```

### 2. Ask the agent

```bash
./agent.sh
```

It creates an environment template that can reach only the tunnel host, a
vault with the caller token, and the demo agent. Then it starts a session,
asks the agent to list the users and to try `DELETE /users/1`, and prints
each command the agent ran, its output, and the answer.

The answer lists the users with `[REDACTED:EMAIL_ADDRESS]`,
`[REDACTED:PHONE_NUMBER]` and `[REDACTED:CREDIT_CARD]`, and reports `403`
and `this agent may only read from the API` for the DELETE. The last line is
`turn: completed`.

### 3. Read the audit trail

```bash
docker compose logs --no-log-prefix sidecar | jq -cR 'fromjson? | select((.kind == "statement" or .kind == "violation") and .direction == "client") | {kind, principal, statement, rule, session: .http.headers["x-openai-session-id"]}'
```

An admitted caller's row names `openai-agents` and the session id it sent:
the OpenAI session, or `run.sh` for step 1's checks. The DELETE rows name
rule `read-only`. Step 1's request without a token is the `anonymous` row
with rule `identity`: a refused caller's row carries no session id.

### 4. Clean up

```bash
./agent.sh --delete && ./run.sh down
```

## Free tier

The sidecar is free. Without a license it enforces one guardrail rule and
one data masking rule per process. The quick start and `values.yaml` use
both: `read-only` and `pii`.

One rule can carry more than one check. `read-only` names seven methods;
`pii` names three entities. These are the Claude template's rules; CI keeps
the guardrail and mask rules of the two templates equal.

A config over the cap does not start. `./run.sh` stops and shows the
sidecar's log, which ends:

```
Error: invalid config:
  - 2 guardrail rules are configured (guardrails: 2) and this process enforces at most 1; merge them into one rule. A license lifts this cap: add one with the license flag, the HOOP_LICENSE environment variable, or the "license" key in the config file. Contact our support at https://help.hoop.dev. ai_analysis rules are counted separately and are not limited
```

Check a config before the next start. The output names the limits. Run it
in `quickstart/` after the first `./run.sh`: it creates the token file that
this command mounts.

```bash
docker compose run --rm sidecar hoop start sidecar --validate
```

The running sidecar re-reads `config/sidecar.yaml`. An edit over the cap is
refused with a warning in the log; the old rules stay in force, and the
next start fails.

A license lifts both caps: [talk to us](https://hoop.dev/meet). In the
quick start, put the license document itself in `quickstart/.env`, in
single quotes: `HOOP_LICENSE='{"payload": ...}'`. A path does not work
there: the file is not in the container. On Kubernetes, set the chart's
`license` value.

## Run it against your API

### Before you start

- A Kubernetes cluster that can publish a `LoadBalancer` Service, and a DNS
  name for it. Below, `SIDECAR_HOST` is that name. Every `kubectl` and
  `helm` call uses the current namespace.
- `helm`, `kubectl`, `openssl`, `curl` 7.76 or later and `jq` 1.6 or later.
- An OpenAI API key with Agents API access.
- `hoophq/hoopsidecar` 1.263.0 or later; `values.yaml` pins 1.263.0. Older
  releases report this template's install source as `other`.

### 1. Make the secrets outside your hoop clone

```bash
T=$PWD/deploy/openai-agents/http    # in your hoop clone
mkdir -m 700 -p ~/hoop-openai && cd ~/hoop-openai
openssl rand -hex 24 > caller-token
read -rs API_TOKEN && printf %s "$API_TOKEN" > api-token && unset API_TOKEN    # your API's own token
export SIDECAR_HOST=sidecar.example.com    # EDIT: the sidecar's public host
```

### 2. Get a TLS certificate

The vault fills secrets only into HTTPS requests on port 443 or 8443, so the
lane serves TLS with a certificate from a public CA, for `SIDECAR_HOST`.

1. Fix the name first. Reserve an address, point `SIDECAR_HOST` at it, and
   add `--set laneServices.public.loadBalancerIP=<address>` in step 3. Or
   use a DNS-01 challenge, which needs no address.
2. Get the certificate. With certbot and DNS-01, for example:

   ```bash
   sudo certbot certonly --manual --preferred-challenges dns -d "$SIDECAR_HOST"
   sudo cat "/etc/letsencrypt/live/$SIDECAR_HOST/fullchain.pem" > tls.crt
   sudo cat "/etc/letsencrypt/live/$SIDECAR_HOST/privkey.pem" > tls.key
   ```

   `tls.crt` holds the full chain, not only the leaf.
3. Then run the sidecar.

### 3. Run the sidecar

```bash
kubectl create secret tls hoop-sidecar-tls --cert=tls.crt --key=tls.key
kubectl create secret generic hoop-caller-token --from-file=tokens=caller-token
helm install hoop-api oci://ghcr.io/hoophq/helm-charts/hoopsidecar-chart --version 1.263.0 \
  -f "$T/values.yaml" --set 'config.listeners[0].upstream=https://api.example.com'    # EDIT: your API
kubectl get svc hoop-api-hoopsidecar-public    # SIDECAR_HOST must resolve to EXTERNAL-IP
```

The lane forwards the `Authorization` header unchanged, so the API gets the
token from `api-token`.

### 4. Check the sidecar

Before any OpenAI step, from your machine:

```bash
curl -sS "https://$SIDECAR_HOST/"
curl -sS -o /dev/null -D - -X POST "https://$SIDECAR_HOST/" \
  -H @<(printf 'X-Hoop-Token: %s\n' "$(cat caller-token)") | grep -i '^x-hoop-denied'
```

The first prints `request refused: the request carried no credential`. The
second prints `x-hoop-denied: policy`.

### 5. Create the OpenAI side

```bash
read -rs OPENAI_API_KEY && export OPENAI_API_KEY
export CALLER_TOKEN_FILE=$PWD/caller-token API_TOKEN_FILE=$PWD/api-token
"$T/setup.sh" create --dry-run    # the request bodies, with your host; sends nothing
"$T/setup.sh" create
```

`create` makes an environment template that reaches only `SIDECAR_HOST`, a
vault with `HOOP_CALLER_TOKEN` and `CUSTOMER_API_TOKEN` bound to that host,
and the agent. It writes their ids to `./.openai-ids`, and refuses a second
run until `delete`.

### 6. Start a session

```bash
"$T/setup.sh" session 'Which plan is customer 2 on?'
```

No variable in the sandbox names its session, so the first message gives
the agent its session id. The script waits for the turn and prints the
agent's commands and answer.

In your own agent, put both tokens in headers only, never in a URL or a
body: toward the sidecar's host the vault fills a placeholder anywhere in
the request. `openai/agent.md` says so to the model.

### 7. Read the audit trail

```bash
kubectl logs deploy/hoop-api-hoopsidecar | jq -cR 'fromjson? | select((.kind == "statement" or .kind == "violation") and .direction == "client")
  | {kind, principal, statement, rule, session: .http.headers["x-openai-session-id"]}'
```

A request row names `openai-agents` and carries the session id the agent
sent. Two kinds of row carry no session id, although the request sent it: a
refused caller (`anonymous`, rule `identity`) and a WebSocket frame.

### 8. Check that the sandbox holds no real token

```bash
"$T/setup.sh" session 'Without printing either value: give the length and SHA-256 of $HOOP_CALLER_TOKEN and $CUSTOMER_API_TOKEN, and of the same two values as curl -v writes them in its > request lines.'
for f in caller-token api-token; do printf '%s %s\n' "$f" "$(tr -d '\n' < "$f" | openssl dgst -sha256 -r | cut -d' ' -f1)"; done
```

The sandbox values must differ from yours. They are placeholders such as
`__OPENAI_SECRET__[HOOP_CALLER_TOKEN:<32 hex digits>]__`, and the egress
proxy swaps in the real tokens on the way out. A request the sidecar admits
shows that the swap happened.

### 9. Clean up

```bash
"$T/setup.sh" delete
helm uninstall hoop-api
kubectl delete secret hoop-sidecar-tls hoop-caller-token
```

`delete` removes the vault, the sessions, the agent and the template. If it
fails halfway, run it again: it retries only what is left. Then remove
`~/hoop-openai`.

A network error during `create` or `session` can leave a resource that
`.openai-ids` does not list. Find it with `GET /v1/vaults`, `/v1/agents`,
`/v1/agents/environments/templates` or `/v1/agents/sessions`, by the name
`hoop sidecar`, `customer-api` or the quick start's `hoop-demo-api`, and
delete the ones you made.

## What to expect

| The agent | Result |
|---|---|
| GETs a customer | `200`, emails, phones and cards read `[REDACTED:EMAIL_ADDRESS]`, `[REDACTED:PHONE_NUMBER]` and `[REDACTED:CREDIT_CARD]` |
| Sends any method but `GET`, `HEAD` or `OPTIONS` | `403`, `X-Hoop-Denied: policy`; the API receives nothing |
| Omits `X-Hoop-Token` | `403`, `request refused: the request carried no credential` |
| Calls the API host, or any other host | curl: `CONNECT tunnel failed, response 403`. A client that skips `HTTPS_PROXY`: `Temporary failure in name resolution` |

## Limits

OpenAI's:

- **US data residency only, no ZDR.** The Agents API keeps data in the
  United States only and does not support Zero Data Retention. The sidecar
  does not change this: the prompts and the masked responses the agent
  reads go to OpenAI.
- **Exact hosts only.** `allowed_domains` takes exact host names: no
  wildcard, scheme, port or path. A redirect to another host fails.
- **An idle sandbox is deleted after one hour.** Its files go with it. The
  timeout is not configurable.
- **No cost cap per session.** The API refuses `spend_control`, the session
  field that the Python SDK 3.37.0 declares.
- **Keep the lane on 443 only.** The egress proxy also lets port 80 through
  to the sidecar's host, unencrypted, and the vault fills nothing there.
- **The vault fills a placeholder anywhere in the request.** Toward the
  sidecar's host it fills the URL and the body too, not only headers, and
  OpenAI has no setting to stop it. A token the agent writes into a URL
  reaches the sidecar's audit row in clear. An API that echoes a value
  returns the real token to the sandbox. The agent's instructions keep the
  tokens in headers; a prompt is not a control.
- **Hosted tools are untested.** `agent.json` lists no tools. Whether
  `allowed_domains` limits a hosted tool such as web search is untested; add
  none.
- **The lane's own certificate is untested from a hosted session.** The
  test sessions reached the lane through a Cloudflare tunnel, so the egress
  proxy saw Cloudflare's certificate, not the one in `hoop-sidecar-tls`.

The sidecar's:

- **The session id is a label.** The agent writes the header, so it can
  write any value. It correlates rows; it does not prove identity.
- **One token for every agent.** Each row names `openai-agents`, not a
  person. Per-user rows need a per-user credential.
- **The rule reads the request method, not an override.** An API that
  honours `X-HTTP-Method-Override`, `X-HTTP-Method` or `X-Method-Override`
  runs a GET that carries one as the method it names. Turn overrides off at
  the API. Or, with a license, add one `http_header` rule per override
  header and list each header in `http.headers`.
- **A request is held until it is whole.** A request over 8 MiB is refused.
  A client that sends `Expect: 100-continue` (curl above 1 MiB) waits about
  1 second for its own timeout before it sends the body.
- **Masking needs a known encoding.** A JSON response under `br` or `zstd` is
  refused, not forwarded unmasked. A response over 16 MiB passes unmasked.
- **REST over HTTP/1.1 and HTTP/2, JSON responses.** Server-sent events,
  Kubernetes and MCP are out of scope for this template.
