# Claude Managed Agents: a REST API through the sidecar

A Claude Managed Agents cloud agent reaches one REST API, and only through a
hoop sidecar. The sidecar admits a caller by token, denies writes, masks
personal data in responses and records each request with the Claude session
id. The docs page:
[Claude Managed Agents](https://hoop.dev/docs/integrations/claude-managed-agents).

```
cloud sandbox, limited networking          your network
+----------------------------+   HTTPS    +--------------------+  HTTPS  +----------+
| agent: curl $SIDECAR_HOST  |--egress--->| hoop sidecar       |-------->| REST API |
| env: vault placeholders    |  proxy     | token, deny, mask, |         +----------+
+----------------------------+  fills the | audit              |
  any other host: refused       secrets   +--------------------+
```

The agent cannot skip the sidecar:

- The environment allows one host, the sidecar's. The egress proxy refuses
  every other host, the API's included.
- The sandbox holds vault placeholders. The egress proxy writes the real
  tokens into request headers, and only toward the sidecar's host.
- The sidecar refuses a request without the caller token before any byte of
  it reaches the API.

## Files

| Path | Holds |
|---|---|
| `values.yaml` | Sidecar chart values: one HTTPS lane, the caller token, a read-only guardrail, a PII mask rule, `analytics.installSource` |
| `ant/` | An `ant apply` project: the environment, the vault, an example agent |
| `credentials/` | The two vault credentials, without their secrets |

## Before you start

- A Kubernetes cluster that can publish a `LoadBalancer` Service, and a DNS
  name for it. Below, `SIDECAR_HOST` is that name.
- A TLS certificate for `SIDECAR_HOST` from a public CA.
- `helm`, `kubectl`, `jq`, and `ant` 1.42.0 or later
  (`brew install anthropics/tap/ant`).
- `ANTHROPIC_API_KEY`: a key with Managed Agents access.
- `hoophq/hoopsidecar` newer than 1.255.0. On 1.255.0 and older, a denied
  request whose body arrives in more than one read reaches the API in part.

## 1. Run the sidecar

```bash
openssl rand -hex 24 > caller-token    # keep both files out of git
read -rs API_TOKEN && printf %s "$API_TOKEN" > api-token && unset API_TOKEN    # your API's own token
kubectl create secret tls hoop-sidecar-tls --cert=tls.crt --key=tls.key
kubectl create secret generic hoop-caller-token --from-file=tokens=caller-token
```

Set `upstream` in `values.yaml` to your API, then:

```bash
helm install hoop-api oci://ghcr.io/hoophq/helm-charts/hoopsidecar-chart -f values.yaml
kubectl get svc hoop-api-hoopsidecar-public    # point SIDECAR_HOST at EXTERNAL-IP
```

## 2. Create the environment, vault and agent

Replace `sidecar.example.com` with `SIDECAR_HOST` in four files:
`ant/environments/hoop-sidecar.yaml`, `ant/agents/customer-api.md` and both
files in `credentials/`. Then:

```bash
cd ant && ant apply .    # writes claude-lock.json; commit it
lockid() { jq -r --arg f "$1" '.resources[$f].id' claude-lock.json; }
AGENT_ID=$(lockid ./agents/customer-api.md)
ENV_ID=$(lockid ./environments/hoop-sidecar.yaml)
VAULT_ID=$(lockid ./vaults/hoop-sidecar.yaml)
```

## 3. Add the two credentials

`ant apply` cannot hold a secret, so each credential is created from its file
plus the secret. `tr` removes the newline the API refuses; `@<(...)` keeps
the secret out of the process list.

```bash
cd ..
ant beta:vaults:credentials create --vault-id "$VAULT_ID" \
  --auth.secret-value @<(tr -d '\n' < caller-token) < credentials/caller-token.yaml
ant beta:vaults:credentials create --vault-id "$VAULT_ID" \
  --auth.secret-value @<(tr -d '\n' < api-token) < credentials/customer-api-token.yaml
```

The sidecar forwards the `Authorization` header unchanged, so the API gets
the token from `api-token`.

## 4. Start a session

The Managed Agents docs name no session id inside a cloud sandbox, so the
first message carries it:

```bash
SESSION_ID=$(ant beta:sessions create --agent "$AGENT_ID" \
  --environment-id "$ENV_ID" --vault-id "$VAULT_ID" \
  --transform id --raw-output)
ant beta:sessions:events send --session-id "$SESSION_ID" --event \
  "{type: user.message, content: [{type: text, text: 'Your session id is $SESSION_ID. Which plan is customer 2 on?'}]}"
ant beta:sessions connect --session-id "$SESSION_ID"
```

## 5. Read the audit trail

```bash
kubectl logs deploy/hoop-api-hoopsidecar | jq -cR 'fromjson? | select(.kind == "statement" or .kind == "violation")
  | {kind, statement, rule, session: .http.headers["x-claude-session-id"]}'
```

A request row carries the session id the agent sent. A refused caller is
recorded under `anonymous` with rule `identity`.

## 6. Check that the sandbox holds no real token

Ask the agent, without printing a value, for the length and SHA-256 of
`$HOOP_CALLER_TOKEN` and `$CUSTOMER_API_TOKEN`, and of the same two values as
`curl -v` writes them in its `>` request lines. Compare with your files:

```bash
for f in caller-token api-token; do printf '%s %s\n' "$f" "$(tr -d '\n' < $f | shasum -a 256 | cut -d' ' -f1)"; done
```

The sandbox values must differ from yours: they are vault placeholders, and
the egress proxy swaps in the real tokens on the way out. A request the
sidecar admits shows that the swap happened.

## What to expect

| The agent | Result |
|---|---|
| GETs a customer | `200`, emails and phones read `[REDACTED:EMAIL_ADDRESS]` and `[REDACTED:PHONE_NUMBER]` |
| Sends any method but `GET`, `HEAD` or `OPTIONS` | `403`, `X-Hoop-Denied: policy`; the API receives nothing |
| Omits `X-Hoop-Token` | `403`, `request refused: the request carried no credential` |
| Calls the API host, or any other host | `Could not resolve host` |

## Limits

- **The session id is a label.** The agent writes the header, so it can
  write any value. It correlates rows; it does not prove identity.
- **One token for every agent.** Each row names `claude-managed-agents`, not
  a person. Per-user rows need a per-user credential.
- **Free tier.** One guardrail rule and one mask rule per process. A license
  lifts both caps.
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
