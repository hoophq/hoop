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

## Files

| Path | Holds |
|---|---|
| `values.yaml` | Sidecar chart values: one HTTPS lane on 443, the caller token, a read-only guardrail, a PII mask rule, `analytics.installSource` |
| `setup.sh` | Creates and deletes the OpenAI side from `openai/`, and starts a session |
| `openai/` | The request bodies: the environment template, the vault, the two credentials without their secrets, the agent (`agent.json`, instructions in `agent.md`), the session |

## Free tier

The sidecar is free. Without a license it enforces one guardrail rule and
one data masking rule per process. `values.yaml` uses both: `read-only`
names seven methods, `pii` names three entities.

These are the Claude template's rules; CI keeps the guardrail and mask
rules of the two templates equal. A config over the cap does not start.

A license lifts both caps: set the chart's `license` value.
[Talk to us](https://hoop.dev/meet).

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

### 7. Read the audit trail

```bash
kubectl logs deploy/hoop-api-hoopsidecar | jq -cR 'fromjson? | select(.kind == "statement" or .kind == "violation")
  | {kind, statement, rule, session: .http.headers["x-openai-session-id"]}'
```

A request row carries the session id the agent sent. A refused caller is
recorded under `anonymous` with rule `identity`.

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
- **The vault fills a placeholder anywhere in the request.** Toward the
  sidecar's host it fills the URL and the body too, not only headers, and
  OpenAI has no setting to stop it. A token the agent writes into a URL
  reaches the sidecar's audit row in clear. An API that echoes a value
  returns the real token to the sandbox. The agent's instructions keep the
  tokens in headers; a prompt is not a control.
- **Hosted tools are untested.** `agent.json` lists no tools. Whether
  `allowed_domains` limits a hosted tool such as web search is untested; add
  none.

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
