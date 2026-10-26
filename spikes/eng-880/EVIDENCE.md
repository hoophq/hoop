# ENG-880 evidence: the sidecar as the only way out of AgentCore agents in a VPC

Tested 2026-10-26 in us-east-1, Sandbox account. Harness and Runtime in VPC mode, agent subnets in `use1-az1` and `use1-az2` with a route table that holds only the local route. Model `us.anthropic.claude-haiku-5-5`. Sidecar `hoophq/hoopsidecar:1.263.0` on ECS Fargate ARM64, with the dev license (more than one rule per process). Agent image `eng880:agent` (Debian 12, `linux/arm64`: psql 15, curl 7.88, dig, boto3). Raw output is in `out/`; ECR tokens in error events are replaced with `<redacted>`.

## Answer

- **The shape holds for both agent kinds.** In agent subnets with no route out, a harness (custom image or base environment) and a Runtime agent reached the API only through the sidecar's http lane: masked GET, refused DELETE, refused caller without the token, one audit row per request with the session id (c3). Every other destination timed out: public IPv4 and IPv6, the upstream API's own address, AWS services without an endpoint. `psql` worked only through the postgres lane (c4).
- **The harness loop runs inside the microVM, beside the agent's container, on the customer VPC.** Its model calls, MCP calls and vault lookups cross the VPC. So an `apiBase` aimed at the internet goes nowhere (c5), an OpenAI model works through a sidecar lane (c9), and the loop does not trust a CA baked into the agent's image (c7).
- **Three ways around the sidecar, two of them closed by config:** the S3 gateway endpoint with its default policy let the agent write to an outside bucket, and a scoped policy refused it; the VPC resolver answers public names, and Route 53 Resolver DNS Firewall closed it; the AWS APIs that have an endpoint stay open to the agent's shell, which holds the execution role (c5).
- **The token vault keeps a token out of the shell, but only for MCP headers and the model key.** The shell cannot fetch it (`WorkloadIdentity is linked to a service`), it is not in `env`, and it reached no log. A token in an environment variable is in `env`, in `GetHarness`, and in CloudWatch the moment the agent prints it: the harness logs message content (c7).
- **One-click is one CloudFormation stack** that also creates the harness: 7 min 37 s to create, 44 s to the first answer (c8). A hoop image on ECR Public fails without NAT; an ECR pull-through cache rule in the stack serves it (c8).

## Table: cell × result

| # | Cell | Result | Evidence |
|---|---|---|---|
| c1 | Harness, no internet: shell command; every endpoint it needs | **Works**. Custom image: `ecr.dkr`, `ecr.api`, S3 gateway, `bedrock-runtime`; `bedrock-agentcore` when a tool or model names a vault key. Base environment: `bedrock-runtime` only | Ablation table below; `out/c1-*` |
| c2 | Runtime agent, no internet: Bedrock + `curl` | **Works**. Per session it needs `bedrock-runtime` only: no ECR or S3 endpoint | `out/c2-rt1-s1.json`, ablation |
| c3 | HTTPS to the lane, audited; public host and upstream fail, and how | **Works**. Lane `200` masked, `403 x-hoop-denied: policy`, `403` without token. Public hosts, the upstream's address, IPv6: **timeout** (`curl: (28)`), names resolve | `out/c3-h1-net.jsonl`, `out/c2-rt1-net.jsonl`, `out/c3-sidecar-audit.jsonl` |
| c4 | `psql` through the postgres lane; direct fails | **Works**. SELECT masked, write `FATAL ... read-only`, direct: `timeout expired`. Session id via `options` lands in the audit row | `out/c4-rt1-pg.jsonl` |
| c5a | S3 gateway endpoint scoped: write to another bucket | **Open by default, closed when scoped**. Default policy: `PutObject` `200`; scoped to the ECR layer bucket: `AccessDenied`, image pulls still work | `out/c7-h3-aws.jsonl`, `out/c5-h1-dns-s3.jsonl` |
| c5b | Execution role from the shell | **Credentials readable** (MMDSv2, `method=iam-role`). OK: Bedrock Converse, ECR auth token, CloudWatch Logs. Timeout: STS, Secrets Manager (no endpoint). Refused: `GetResourceApiKey` | `out/c7-h3-aws.jsonl` |
| c5c | `apiBase` / `additionalParams` redirect | **No way out**. `apiBase` at a public address: no byte in 240 s, the public probe saw nothing. `apiBase` at a sidecar lane: works (c9). `additionalParams` not tested | `out/c5-h5-apibase-public.*` |
| c5d | DNS: public names answered? DNS Firewall closes it? | **Answered** (`example.com`, random names reach the authoritative servers). **Closed** by a Resolver DNS Firewall allow list: `NXDOMAIN` | `out/c3-h1-net.jsonl`, `out/c5-h1-dns-s3.jsonl` |
| c5e | `requireServiceS3Endpoint` (extra) | **Cannot be enabled** on a new agent: CreateHarness refuses the field, UpdateHarness fails for agents created after 2026-06-11 | Below |
| c6 | Session id visible in the microVM | **Harness: no** (not in `env`, files, or the tool's shell); the first message gives it. **Runtime: yes**, header `x-amzn-bedrock-agentcore-runtime-session-id` to the container | `out/c6-oc-env.jsonl`, `out/c2-rt1-s1.json` |
| c7 | Env variable vs vault key in an MCP header | **Vault is least exposed**: filled toward the lane (`<REAL:api_token>`), not in `env`, not fetchable by the shell, in no log. Env: in `env`, in `GetHarness`, in CloudWatch when printed. Vault covers MCP headers and model keys only | `out/c7-h3-*` |
| c8 | One stack: VPC, endpoints, Fargate sidecar, Harness in VPC mode with a custom image; image from hoop? | **Works**. Image: this account's ECR works; ECR Public directly **fails**; ECR Public through a pull-through cache **works**. Cross-account: not tested (out of v1) | `stack/oneclick.yaml`, `out/c8-*` |
| c9 | Non-Bedrock model through a lane | **Works**: LiteLLM `openai/gpt-5.4-mini`, `apiBase` = plain HTTP lane, key from the vault; each model call audited; key in no log | `out/c9-h4-lane.jsonl` |
| c10 | Setup tool | **Works**: `curl --aws-sigv4` 8.17 + `evstream.py`. Not `jq` alone (binary eventstream), not macOS curl 8.7.1 for runtime paths | `ac.sh`, every session |
| c11 | Idle cost | **≈ $0.095 per hour** (≈ $69 per 30 days), 4 interface endpoints in 2 AZs + Fargate sidecar | Below |

## Evidence per cell

### c1, c2. No internet, harness and Runtime: the endpoints each one needs

Baseline, all endpoints on: the harness with the custom image (`h1`), the harness with the base environment (`h2`) and the Runtime agent (`rt1`) each ran `echo shell-ok` through the model's shell tool (`out/c1-baseline-*`). Then one endpoint off at a time, a new session on each (`cells/ablate.sh`, `out/c1-ablation.txt`):

| Endpoint off | `h1` harness, custom image | `h2` harness, base environment | `rt1` Runtime agent |
|---|---|---|---|
| none (baseline) | works, 7 s | works, 32 s | works, 3 s |
| `ecr.dkr` | **fails**: `runtimeClientError` `ctr ... images pull ... eng880:agent returned non-zero exit status 1`, 32 s | works | works |
| `ecr.api` | **fails**: `Connect timeout on endpoint URL: "https://api.ecr.us-east-1.amazonaws.com/"`, 183 s | works | works |
| S3 gateway | **fails**: `ctr ... images pull` exit 1, 151 s | works | works |
| `bedrock-runtime` | **fails**: no byte, stream closed after 150 s | **fails**: no byte in 300 s | **fails**: no byte in 300 s |
| `logs` | works | works | works |
| `bedrock-agentcore` (`h3`, vault key in an MCP header) | **fails**: no byte in 300 s; works with it | n/a | n/a |

- A harness pulls a custom image inside the microVM at each session start: `ctr images pull ... 259815801592.dkr.ecr.us-east-1.amazonaws.com/eng880:agent`. So `ecr.dkr`, `ecr.api` and the S3 gateway are needed per session, not only at create.
- The `runtimeClientError` for a failed pull carries the `ctr` command line, with the ECR authorization token (`--user AWS:<token>`), to the caller of InvokeHarness.
- The base environment and the Runtime agent did not need ECR or S3 per session. Where AWS pulls those images from was not traced.
- Without `bedrock-runtime` nothing fails fast: the custom-image harness closed the stream after 150 s with no byte (`unexpected eof`), the other two sent no byte in 300 s. The harness loop's model calls cross the customer VPC.

### c3. HTTP through the lane, and everything else

`cells/net.sh` through InvokeAgentRuntimeCommand, harness `h1` (`out/c3-h1-net.jsonl`) and Runtime `rt1` (`out/c2-rt1-net.jsonl`), same result on both:

```
== c3 lane (demo API)
HTTP/2 200
  "email": "[REDACTED:EMAIL_ADDRESS]",
  "phone": "[REDACTED:PHONE_NUMBER]",
  "card": "[REDACTED:CREDIT_CARD]",
HTTP/2 403
x-hoop-denied: policy
this agent may only read from the API
HTTP/2 403
request refused: the request carried no credential
== c3 outside the lane
https example.com                  exit=28  000  8.001941s
https 1.1.1.1 (IP)                 exit=28  000  8.001520s
https api.openai.com               exit=28  000  8.001373s
probe direct :8088                 exit=28  000  8.000965s
IPv6 [2606:4700:4700::1111]        exit=28  000  8.001526s
IPv6 ipv6.google.com               exit=6   000  0.015750s
bedrock-agentcore (no endpoint)    exit=28  000  7.999608s
sts global                         exit=28  000  8.001267s
```

- Every refused path is a timeout (`curl: (28)`), not a DNS error or a refusal: names resolve (c5), packets go nowhere. The upstream itself (the probe, `10.88.10.191:8088`) times out too: its security group admits the sidecar only.
- The microVM has two link-local interfaces (`169.254.0.2`, `169.254.1.2`) and a public IPv6 address (`2600:1f18:...`) although the VPC has no IPv6. IPv6 to a literal address timed out; no AAAA name resolved.
- Audit rows (`out/c3-sidecar-audit.jsonl`), Runtime session:

```
statement agentcore  GET /users/1     session eng880-243884b1199cd31884d519f1f419b875
violation agentcore  DELETE /users/1  read-only  session eng880-243884b1199cd31884d519f1f419b875
violation anonymous  GET /users/1     identity   (no session)
```

Model-driven: the Runtime agent ran `curl` to the lane (masked) and to `example.com` (`curl: (28) Connection timed out after 8001 milliseconds`) in one 19 s invocation (`out/c2-rt1-s1.json`). The harness transcript is c8's.

### c4. Postgres through the lane

`out/c3-h1-net.jsonl`, `out/c4-rt1-pg.jsonl`:

```
psql lane select                   exit=0   2|[REDACTED:EMAIL_ADDRESS]
psql lane delete                   exit=2   connection to server was lost
psql direct db:5432                exit=2   psql: error: connection to server at "db.eng880.internal" (10.88.10.175), port 5432 failed: timeout expired
FATAL:  this connection is read-only; writes are not permitted
sslmode=require through the lane: server does not support SSL, but SSL was required
```

- `options='-c agentcore.session.id=<id>'` in the DSN lands in each audit row: `"postgres.option.agentcore.session.id":"eng880-243884b1..."`. ENG-885's plan works.
- The base harness environment has no `psql` (Amazon Linux, `curl` and `/opt/amazon/bin/python3` only); the custom image is required.

### c6. Session id

- Harness: no variable or file in the microVM holds it, in a command (`out/c1-h1-look.jsonl`) or in the model's own shell tool (`out/c6-oc-env.jsonl`). The tool's shell is a fresh `bash -c` with parent PID 0 next to `sleep infinity`; the harness loop does not run in the container. The first message gives the agent its id, as on Claude and OpenAI.
- Runtime: the container gets the header `x-amzn-bedrock-agentcore-runtime-session-id` on each `/invocations` (`out/c2-rt1-s1.json`). The agent code passes it on.

### c8. One-click

`stack/oneclick.yaml`, one `aws cloudformation deploy`, nothing before or after: VPC, endpoints, a generated caller token (Secrets Manager), the sidecar config (SSM), the sidecar on Fargate, and an `AWS::BedrockAgentCore::Harness` in VPC mode with a custom image. One harness session (`out/c8-oc-s1.jsonl`, `out/c8-oc-audit.jsonl`):

```
GET  /users/1 -> 200, "email": "[REDACTED:EMAIL_ADDRESS]", "phone": "[REDACTED:PHONE_NUMBER]", "card": "[REDACTED:CREDIT_CARD]"
DELETE /users/1 -> 403, this agent may only read from the API
curl -m 8 https://api.github.com -> exit_code=28
statement agentcore GET /users/1                session eng880-6056c975b1834a6e7a7cda0d07fec316
violation agentcore DELETE /users/1  read-only  session eng880-6056c975b1834a6e7a7cda0d07fec316
```

Image source, in the same VPC:

| Image | Result |
|---|---|
| This account's ECR (`eng880:agent`) | **Works** |
| `public.ecr.aws/docker/library/python:3.13-slim-bookworm` directly | **Fails** after 206 s: `Connect timeout on endpoint URL: "https://api.ecr-public.us-east-1.amazonaws.com/"` |
| The same image through an ECR pull-through cache rule (`ecr-public` → `public.ecr.aws`) in the stack | **Works**, 40 s first session; ECR created `ecr-public/docker/library/python` |
| Cross-account private ECR | Not tested: out of scope for v1 (project lead, 2026-10-26) |

- The lane is plain HTTP inside the VPC: a stack has no way to give the agent a CA that the sidecar makes at boot. c3 used HTTPS with a CA baked into the image.
- The harness holds the caller token in `EnvironmentVariables`, resolved from the generated secret at deploy: anyone who can `GetHarness` reads it.
- The pull-through cache rule needs `ecr:BatchImportUpstreamImage` and `ecr:CreateRepository` on `repository/ecr-public/*` for the execution role. An account holds one rule per prefix.

### c10. Setup tool

`ac.sh` (`curl --aws-sigv4` + `evstream.py`) invoked every harness and Runtime session above.

- The answer is `application/vnd.amazon.eventstream` (binary frames), so `jq` alone cannot read it; `evstream.py` (50 lines, stdlib) turns it into JSON lines.
- macOS curl 8.7.1 signs an encoded ARN in the path wrong: `403 The request signature we calculated does not match`. curl 8.17.0 signs it right. InvokeHarness takes the ARN in the query and works with both.
- InvokeAgentRuntimeCommand on a harness takes the harness ARN; the harness's runtime id is refused (`managed by a harness and cannot be invoked directly`), and the harness id resolves to a runtime that does not exist (`404`).
- A harness runs the command in bash. A Runtime agent splits it like argv and runs the first word with no shell: pipes, quotes and newlines are lost. `bash -c '<base64> | base64 -d | bash'` works on both.
- The AWS CLI 2.36.15 has `create-harness` but no `invoke-harness` and no `invoke-agent-runtime-command`.

### c11. Idle cost

AWS Price List API, us-east-1, 2026-10-26: interface endpoint $0.01 per AZ-hour; Fargate ARM $0.03238 per vCPU-hour and $0.00356 per GB-hour; public IPv4 $0.005 per hour; NAT gateway $0.045 per hour (for comparison).

| Item (one-click stack) | Per hour |
|---|---|
| 4 interface endpoints (`ecr.dkr`, `ecr.api`, `bedrock-runtime`, `logs`) × 2 AZ | $0.080 |
| Sidecar, 0.25 vCPU + 0.5 GB ARM | $0.0099 |
| Sidecar's public IPv4 | $0.005 |
| S3 gateway endpoint, harness at rest | $0 |
| **Total** | **≈ $0.095 (≈ $69 per 30 days)** |

Not in the table: the caller token secret ($0.40 per month), Cloud Map's private zone ($0.50 per month), logs, and per-session Runtime and model charges. One AZ for the endpoints: ≈ $0.055 per hour.

### c5. Ways around the sidecar

**S3.** The agent role may write to the other bucket, as an attacker's bucket would admit anyone (`OtherBucketPolicy`). From the harness shell (`cells/aws.sh`):

```
default endpoint policy:  s3 PutObject eng880-base-otherbucket-...  OK   200
scoped endpoint policy:   s3 PutObject eng880-base-otherbucket-...  DENY AccessDenied
```

The scoped policy allows only `s3:GetObject` on `arn:aws:s3:::prod-us-east-1-starport-layer-bucket/*`. A new `h1` session pulled its custom image under it (`shell-ok`, 7 s): no other bucket is needed.

**Execution role** (`out/c7-h3-aws.jsonl`). The microVM's metadata service answers `401` without a token and `200` with an MMDSv2 token; boto3 finds the role (`method=iam-role key=ASIA... token=yes`).

```
sts GetCallerIdentity (regional)          FAIL Connect timeout on endpoint URL: "https://sts.us-east-1.amazonaws.com/"
bedrock-runtime Converse                  OK
ecr GetAuthorizationToken                 OK
logs DescribeLogGroups                    OK
secretsmanager GetSecretValue (vault)     FAIL Connect timeout on endpoint URL: "https://secretsmanager.us-east-1.amazonaws.com/"
bedrock-agentcore GetResourceApiKey       DENY ValidationException: WorkloadIdentity is linked to a service and cannot retrieve an access token by the caller.
```

Every endpoint in the VPC is an API the agent can call with the role. What the role allows there is the limit: here, any Bedrock model, ECR pulls, writes to CloudWatch Logs.

**`apiBase`.** Harness `h5`: LiteLLM model, a fake vault key, `apiBase` = the probe's public address (`http://98.80.200.93:8088/v1`, open to the internet for the test). No byte in 240 s; the probe logged only the operator's own request. `h4` with `apiBase` = the sidecar lane: works (c9). `additionalParams` was not tested.

**DNS** (`cells/dns.sh`). Without a firewall the resolver answers public names and forwards unknown ones (`eng880-c5-<n>.example.com` got `NXDOMAIN` from `example.com`'s servers): a query can carry data out. With a Resolver DNS Firewall rule group (allow `*.amazonaws.com`, `*.eng880.internal`, the sidecar's upstreams; block `*` with `NXDOMAIN`):

```
example.com                     status: NXDOMAIN
eng880-c5-29782.example.com     status: NXDOMAIN
api.openai.com                  status: NOERROR  (allowed: the sidecar's upstream)
bedrock-runtime.us-east-1...    status: NOERROR  10.88.1.153
sidecar.eng880.internal         status: NOERROR  10.88.10.86
lane after the firewall: 200
```

The rule group applies to the whole VPC, the sidecar included: the allow list must name the sidecar's upstreams, and the agent can resolve those names too (it still cannot reach them).

**`requireServiceS3Endpoint`.** CreateHarness: `requireServiceS3Endpoint cannot be set during harness creation. This field can only be set via UpdateHarness.` UpdateHarness to `true`: `UPDATE_FAILED`, `Agents created after 2026-06-11T00:00:00Z cannot modify requireServiceS3Endpoint.` The harness kept serving (`shell-ok` after the failed update). A new runtime reports `requireServiceS3Endpoint: false` and `requireMMDSV2: true`.

### c7. Upstream token: environment variable vs vault

Harness `h3`: no token in `environmentVariables`; a `remote_mcp` tool on the probe lane with headers `X-Hoop-Token: ${<vault arn eng880-caller>}` and `x-api-key: ${<vault arn eng880-api>}`.

```
MCP whoami through the lane:  {"mcp_call_src_ip": "10.88.10.86", "user_agent": "python-httpx/0.28.1"}
sidecar:  statement agentcore POST /mcp        (admitted: the vault filled X-Hoop-Token)
probe:    x-api-key {"len": 48, "value": "<REAL:api_token>", "real": true}
shell:    env | grep -ciE 'token|api_key|secret'  ->  1  (TIKTOKEN_CACHE_DIR)
```

- The fill needs the `bedrock-agentcore` endpoint: without it the session sent no byte in 300 s.
- The MCP client is the harness loop, outside the agent's container: an HTTPS lane with the CA baked into the image failed, `CERTIFICATE_VERIFY_FAILED: unable to get local issuer certificate`. The test used a plain HTTP lane (`probe-plain`, port 8446).
- The vault reads its Secrets Manager secret with the execution role: without `secretsmanager:GetSecretValue`, `GetResourceApiKey` failed (`Access denied when retrieving secret`). With the condition `aws:CalledVia = bedrock-agentcore.amazonaws.com` it worked; that condition keeps the shell, which holds the same role, from reading the secret if Secrets Manager were reachable. The direct read was not tested: the VPC has no Secrets Manager endpoint.
- Logs: neither vault value was found in any log group (filter on the literal value, every group, 4 hours).
- Environment variables (`h1`, the one-click harness): the shell reads them, `GetHarness` returns them, and the one-click session that ran `env` put `HOOP_CALLER_TOKEN=<value>` into `/aws/bedrock-agentcore/runtimes/harness_eng880_oneclick-.../otel-rt-logs` twice: the harness sets `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true` and logs each tool result. X-Ray had no traces (no `xray` endpoint).

Least exposed: the vault, for what it covers. It does not cover the agent's own HTTP calls (`curl` to the lane), which need the token in the environment (ENG-886).

### c9. A non-Bedrock model

`h4`: `liteLlmModelConfig {modelId: openai/gpt-5.4-mini, apiKeyArn: <vault eng880-openai>, apiBase: http://sidecar.eng880.internal:8445/v1}`, lane `openai` to `https://api.openai.com`, no caller token, no rule.

```
$ echo shell-ok
shell-ok
I'm ChatGPT.
sidecar: statement anonymous POST /v1/chat/completions -> 200 OK   (one pair per model turn)
```

The OpenAI key was found in no log group. The lane was plain HTTP: it was switched before this cell, once c7 showed that the harness loop does not trust the image's CA; an HTTPS lane was not tried for model calls. Only LiteLLM was tested. CloudFormation's `OpenAiModelConfig` has `ApiBase`; the CLI 2.36.15 skeleton does not.

## Facts from the ticket, checked

| Fact | Result |
|---|---|
| Harness GA in every AgentCore region | Not tested beyond us-east-1, where it worked |
| Harness runs on the public network by default; set `networkMode: VPC` | Not tested: every harness set `VPC` |
| VPC mode needs `ecr.dkr`, `ecr.api`, S3 gateway, `bedrock-runtime` | **Confirmed for a custom image.** The base environment needs only `bedrock-runtime`; a vault reference adds `bedrock-agentcore` |
| Execution role needs pull rights on AWS's `harness-*` repository | In the role; not tested without it |
| Base environment: Python and bash only | Amazon Linux with `bash`, `curl` and `/opt/amazon/bin/python3`; no `psql`, no `dig` |
| Harness overrides `ENTRYPOINT` and `CMD` | **Confirmed**: PID 1 is `/bin/sh -c sleep infinity` in the custom image |
| `InvokeAgentRuntimeCommand` runs as root | **Confirmed** (`uid=0`), in bash on a harness, without a shell on a Runtime agent |
| Session id 33 to 100 characters | Used 39; limits not tested |
| VPC mode filters by security group; only a NAT gives internet | **Confirmed**: every path the SG did not open timed out; the agent subnets have no NAT |
| Docs: Runtime needs the `logs` endpoint | **Not as documented**: sessions ran without it. Whether their logs arrived was not checked |

## Recommendation

**Confirm the shape.** The customer's VPC enforces it, and both agent kinds obey it. Five settings make "only through the sidecar" true, and milestone 2 must ship all five:

1. Agent subnets with no route out; the agent SG reaches only the lanes, the endpoints and the S3 prefix list.
2. Endpoints: `ecr.dkr`, `ecr.api`, S3 gateway, `bedrock-runtime`; `bedrock-agentcore` only when a tool or model names a vault key. The endpoint SG admits the whole VPC: private DNS covers the Fargate tasks too.
3. The S3 gateway endpoint policy allows only `s3:GetObject` on `prod-<region>-starport-layer-bucket`.
4. A Route 53 Resolver DNS Firewall rule group on the VPC: allow `amazonaws.com`, the private namespace and the sidecar's upstream hosts; block the rest with `NXDOMAIN`.
5. An execution role with no more than the harness needs: every endpoint is an API the shell can call with it.

**One-click on AWS** is a Launch Stack link to one template that creates the network, the sidecar and the harness, about 8.5 minutes to the first answer. The agent image comes from ECR Public through a pull-through cache rule in the stack; hoop publishes the image, the customer builds nothing.

**Lane TLS.** The harness loop's MCP client did not trust the CA baked into the image (c7); the agent's own `curl` did. A public-CA certificate on a lane was not tried, nor HTTPS for model calls. v1: plain HTTP inside the VPC, behind the security groups; the README says so. A public certificate needs a domain the customer owns: not one-click.

**Tokens.** v1 keeps the caller token in an environment variable (the project lead's call). The README must say that `GetHarness` returns it and that anything the agent prints lands in CloudWatch. MCP tools and non-Bedrock model keys use the vault.

### Milestone 2 to 6 tickets that must change

| Ticket | Change |
|---|---|
| ENG-882 REST template | DoD endpoints: the c1 list above, not "c1 and c2 list"; add the S3 policy, the DNS Firewall and the endpoint SG rule; the CI egress check also fails on an open S3 endpoint policy and on a VPC without the firewall association. Checkpoint answers: config in SSM, caller token generated in Secrets Manager and resolved into the harness env (c8); image from ECR Public via pull-through cache (prefix as a parameter: one rule per prefix per account). Lane is plain HTTP in the VPC. Memory off (`Memory: {Disabled: {}}`): managed memory was not tested in VPC mode. Session id: harness, first message; Runtime, the header the container gets. `setup.sh` needs curl 8.17 or later, or the Docker curl, and an eventstream decoder; `jq` alone cannot read the answer. README limits: c5b (role APIs open to the shell), c7 (env token in CloudWatch), c9, c11 |
| ENG-883 Kubernetes | The sidecar resolves the cluster endpoint through the same DNS Firewall: add it to the allow list. `kubectl` runs in the agent's container, so a CA in the image works for it |
| ENG-884 quick start | "The sidecar has no egress" does not hold: the Fargate task pulls `hoophq/hoopsidecar` from Docker Hub (public IP here, $0.005 per hour), or the stack adds a pull-through cache for it. Cold time ≈ 8.5 min (stack 7 min 37 s, harness 4.5 of it; first answer 44 s). Cost ≈ $0.095 per hour |
| ENG-885 Postgres | Holds as written: `options=-c agentcore.session.id=<id>` lands in the audit row; the base environment has no `psql`; the deny closes the connection after `FATAL ... read-only` (ENG-800); `sslmode=require` fails without `downstream_tls`. A Postgres container on Fargate worked for the test database |
| ENG-886 credential holding | Context: "On AgentCore there is no vault" is wrong. The token vault fills MCP tool headers and model API keys, out of the shell's reach (c7). It does not cover the agent's own HTTP calls, so the ticket stays |
| ENG-887 docs page | Add: plain HTTP lane in the VPC; DNS and S3 settings; OpenAI and other LiteLLM models work through a lane (c9); tokens printed by the agent land in CloudWatch |
| ENG-888 brief | AgentCore row: the customer's VPC enforces (security groups, no route, DNS Firewall), not a vendor proxy; both Bedrock and OpenAI models |

## Known issues found

| # | Issue | Where |
|---|---|---|
| K1 | A failed custom-image pull returns the `ctr` command line, with an ECR authorization token (`--user AWS:<token>`), in the `runtimeClientError` event to the InvokeHarness caller | AgentCore harness |
| K2 | A missing endpoint (`bedrock-runtime`, `bedrock-agentcore`) makes a session hang: no byte for 150 to 300 s, no error | AgentCore |
| K3 | The harness logs every tool result to CloudWatch (`otel-rt-logs`): a token the agent prints is stored there | AgentCore harness |
| K4 | `InvokeAgentRuntimeCommand` runs the command in bash on a harness and without a shell on a Runtime agent; the docs say bash | AgentCore |
| K5 | Private DNS of an interface endpoint covers the whole VPC. An endpoint SG that admits only the agents stopped a new Fargate sidecar (`cannot find the log group`) and held the old task's logs until the SG changed | Template |
| K6 | The microVM has a public IPv6 address (`2600:1f18:...`) in a VPC with no IPv6. It reached nothing in these tests | AgentCore |
| K7 | A failed UpdateHarness leaves the harness in `UPDATE_FAILED`; it keeps serving | AgentCore |
| K8 | macOS curl 8.7.1 signs an encoded ARN in a path wrong; the AWS CLI 2.36.15 has no `invoke-harness` | Tooling |

## Reproduce

- `deploy.sh up` deploys `stack/base.yaml` (VPC, toggled endpoints, sidecar, probe, Postgres) with `stack/sidecar.yaml`; `deploy.sh params Key=Value` toggles; `deploy.sh down` removes it.
- `agents.sh harness|runtime <name>` with `IMAGE`, `MODEL`, `TOOLS`, `ENVTOKENS`, `APIBASE`; `ac.sh` invokes; `out.sh` and `transcript.sh` render.
- `cells/`: `look.sh`, `net.sh`, `pg.sh`, `aws.sh`, `dns.sh` run in the microVM through `ac.sh cmd`; `ablate.sh` runs c1 and c2.
- `stack/oneclick.yaml`: c8, alone.
- `agent/`: the image for both agent kinds. `stack/probe_server.py`: ENG-860's probe, secrets from the environment.
- `teardown.sh` removes all of it except the access StackSet, which runops revokes.
- Lifetime: base stack 18:14 to 20:46 UTC, one-click stack 18:53 to 20:46 UTC, at the c11 rates about $0.50 together, plus Runtime session time and model tokens (Haiku 5.5, gpt-5.4-mini; not measured).
