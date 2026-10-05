# sidecar

> **0.1.0**: the API is settling. The config schema moved in this release.
> `policy` split into `guardrails` and `opa`, `mask.enabled` went away, and two
> defaults reversed. [Deprecated fields](#deprecated-fields) carries the
> migration table and the three behaviour changes that can move traffic. The
> Go interfaces will keep moving.

Turn wire protocols into structured statements, and structured statements
into allow/deny verdicts.

**Relay codecs are pure functions over bytes.** The root library opens no
socket, terminates no TLS, and routes nothing. You hand it bytes you already
have, and whatever holds the connection keeps holding it.

**The daemon owns protocol transports.** Database and HTTP lanes accept one
connection, dial one upstream, and pump bytes through a codec-backed gate. An
http lane that receives HTTP/2 bridges each stream into that same HTTP/1.1
relay rather than inspecting a second wire format.
The gRPC lane is the deliberate exception: it terminates HTTP/2 in-process,
applies policy and descriptor-backed protobuf masking per stream, then proxies
the RPC upstream. This keeps one denied stream from closing unrelated RPCs on
the same connection. It runs standalone; Envoy is optional.

**One root dependency.** `github.com/hoophq/libhoop` carries the protocol
decoders, the gRPC HTTP/2 endpoint, and the wire types this module aliases.
The root module declares only libhoop. Sidecar-specific gRPC identity,
statement, policy, audit, and masking behavior is injected from `daemon/`
into `libhoop/v2/codec/grpc`. libhoop is private, so builds need
`GOPRIVATE=github.com/hoophq/libhoop` and credentials.

The root shipped zero dependencies until the decoders moved to libhoop. They
produce the `Statement` a policy evaluates, so this module cannot describe its
own inputs without naming their types.

**Go 1.26.5.** Every module here declares `go 1.26.5`, so an older toolchain
refuses the build instead of miscompiling it. The repo's `go.work` and the
sidecar image pin the same version.

```go
import (
    "github.com/hoophq/hoop/sidecar/inspect"
    "github.com/hoophq/hoop/sidecar/policy"
)

insp, _ := inspect.New(inspect.Postgres)
rules, _ := policy.NewRules([]policy.Rule{{
    Name:       "no-destructive",
    Type:       policy.MatchOperation,
    Operations: []inspect.Operation{inspect.OpDelete, inspect.OpDrop},
    Message:    "destructive statements are not permitted on appdb",
}})

stmts, _ := insp.Inspect(inspect.FromClient, packetBytes)
for _, s := range stmts {
    if v := rules.Evaluate(s); v.Denied {
        return errors.New(v.Message) // surface it in the protocol's error frame
    }
}
```

## Run it locally: the Envoy stack

The fastest way to watch all of this work is the compose stack in
[`deploy/docker-compose/envoy-stack`](../deploy/docker-compose/envoy-stack). Envoy
terminates TLS and calls OPA for reachability, `hoop-inspect` sits behind it as
an ordinary upstream, and a Postgres database and an HTTP service sit behind
that. No hoop gateway, no agent, no control-plane database: the sidecar reads
one YAML file.

Needs `docker`, `curl`, `openssl` and `python3`.

**1. Bring it up.** The first run takes about a minute. It mints a self-signed
cert for Envoy, builds `hoop-inspect:local` from the `sidecar` tree, and
starts six containers. From the repo root:

```bash
cd deploy/docker-compose/envoy-stack
./run.sh
```

**2. Check the sidecar is healthy**, and read what each lane resolved:

```bash
curl -s localhost:19000/healthz                        # ok
curl -s localhost:19000/config | python3 -m json.tool
```

**3. Walk the lanes.** `./demo.sh` runs every one of these in order and prints
the audit trail at the end. The individual pieces:

```bash
# Tier 1: OPA answers reachability and nothing else.
curl -sk https://localhost:8443/json -H 'X-Hoop-User: alice' -o /dev/null -w '%{http_code}\n'  # 200
curl -sk https://localhost:8443/json -H 'X-Hoop-User: bob'   -o /dev/null -w '%{http_code}\n'  # 403

# Tier 2, postgres. Envoy forwarded these bytes as opaque TCP and consulted
# nobody, so the sidecar is the only thing that can judge them.
PG="docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
  psql -h envoy -p 5432 -U appuser -d appdb"
$PG -c 'DELETE FROM customers WHERE id = 1;'      # refused in a real pgwire ErrorResponse
$PG -c 'SELECT name, email, ssn FROM customers;'  # the result set comes back masked

# Tier 2, http: a denial on the RESPONSE, which no ext_authz config can express.
curl -sk https://localhost:8443/status/503 -H 'X-Hoop-User: alice' -w '\n%{http_code}\n'  # 403
```

**4. Read the evidence** the sidecar recorded:

```bash
curl -s localhost:19000/stats                  | python3 -m json.tool
curl -s 'localhost:19000/api/sessions?limit=1' | python3 -m json.tool
docker compose logs hoop-inspect | ./sidecar/read-audit.py
```

**5. Rebuild after changing this library**, and tear down when you are done:

```bash
./run.sh --rebuild
./run.sh down          # includes volumes
```

| Port | Serves |
|---|---|
| 8443 | Envoy HTTPS, to the `httpbin` lane |
| 5433 | Envoy TCP, to the `appdb` lane |
| 19000 | sidecar admin: `/healthz`, `/stats`, `/config`, `/events`, `/api/*` |
| 9901 | Envoy admin |

That Postgres listener is `envoy:5432` inside the compose network and `5433` on
the host, because a laptop usually has something on 5432 already. It is why the
`psql` line above runs from the `client` container.

`PGSSLMODE=disable` is required on the CLIENT: nothing terminates TLS between
psql and the sidecar on that lane, so a client negotiating it would leave no
plaintext to parse.

The hop from the sidecar to `appdb` IS encrypted, and separately so. See
[Upstream TLS](#upstream-tls).

For the code path behind each command, a per-command runbook and a
troubleshooting table, read
[ADR-0005](https://github.com/hoophq/adr/blob/main/0005-sidecar-flow.md).

## Running the relay yourself

Two ways to run the same relay.

**Through the hoop CLI**, which links the same plugins into the binary the
release pipeline already builds. Nothing extra to compile or ship:

```bash
hoop start sidecar --config config.yaml --validate   # check the config and exit
hoop start sidecar --config config.yaml              # run
hoop start sidecar --config config.yaml --license /etc/hoop-inspect/license.json
```

The command was named `inspect`. On 1.149.0 and newer that name still works as
a deprecated alias and prints a notice naming the new one. **Below 1.149.0 only
`inspect` exists**, so a pinned older image needs the old spelling.

`--config` also reads `HOOP_SIDECAR_CONFIG` (or the older
`HOOP_INSPECT_CONFIG`, still honoured), which is the shape a Kubernetes
deployment wants: mount the ConfigMap, set the variable, pass no arguments.

That binary is already in the images you pull: `hoophq/hoopdev` (agent) and
`hoophq/hoop` (gateway), from **1.126.0** onward as `inspect` and **1.149.0**
onward as `sidecar`. Running the relay out of one
without building anything (`docker exec` into a live container, extending the
image and swapping `CMD`, or adding a second container to the agent pod) is
[sidecar-binary.md](../deploy/docker-compose/envoy-stack/sidecar-binary.md).

**As a standalone binary**, when a sidecar container should carry the relay and
nothing else. It lives in the nested `cmd` module, which is where the optional
plugins get linked (the YAML front end and alcatraz PII detection) so they stay
out of the root's dependency set:

```bash
cd cmd
go build -o hoop-inspect .

./hoop-inspect -validate -config config.yaml
./hoop-inspect -config config.yaml
./hoop-inspect -config config.yaml -license /etc/hoop-inspect/license.json
./hoop-inspect -version
```

Go 1.26.5 or newer builds it. Below that the module's own `go` directive stops
the build with `requires go >= 1.26.5`, and `GOTOOLCHAIN=auto` (the default)
fetches the right one rather than failing.

As a container, with the `sidecar` tree as the build context. From the repo
root:

```bash
docker build -f deploy/docker-compose/envoy-stack/sidecar/Dockerfile \
  -t hoop-inspect:local sidecar/
```

There are no build tags. The config file decides every capability, so an
operator turning on PII detection does not also have to swap the binary. Omit
the `pii` section and the detector still runs, holding all 54 entity types:
the section narrows that set rather than switching it on. A binary built
without the alcatraz plugin is the case that refuses a `mask` block at
startup.

To embed the relay in your own process, call `daemon.Setup` to load the config
and build the detector, then `daemon.Run`. That is all `hoop start sidecar`
does; read `client/cmd/startsidecar.go` for the whole of it.

`Setup` takes the same three arguments it always has. It resolves a license
from `HOOP_LICENSE` and from the config file's `license` key on its own, so an
embedder that mounts one needs no code change. A caller with its own license
flag reaches for `daemon.SetupWith`, which is the same function plus options:

```go
cfg, det, err := daemon.SetupWith(path, configyaml.Load, buildPlugin,
    daemon.WithLicense(licenseFlag))
```

Startup facts arrive as new `Option` values rather than new arguments, so
neither signature moves again. `daemon.Setup` briefly took the license as a
fourth argument, which broke every embedder that upgraded; that is what the
split is for.

### Editing the config file of a running process

A standalone process watches its config file: it stats the path every ten
seconds and re-reads it when the size or mtime moves, or at once on `SIGHUP`
(`kill -HUP <pid>`, `docker kill -s HUP <container>`). An edit that only
touches rules (guardrails, masking, pii, OPA, a listener's analyzer block,
the `license` key) is applied in place, logged as `config file configuration
applied` with a generation number. Connections already open drain under the
rules they were accepted with; new connections run the new rules, and
nothing rebinds or drops. An edit beyond the rules (listeners, audit, admin,
log_level, the top-level analyzer section, adding `control_plane_url`) logs
`restart to apply it` once and the process keeps serving what it had; a file
that does not load keeps the running rules, is retried on the next tick, and
warns once until it loads again. gRPC, Spanner and SSH lanes swap rules the
same way, per RPC or per connection; turning masking on or off on one of them
is the exception and logs `restart to apply it`, because it is a server option
fixed at build. ADR-0014 records the boundary; the control plane's heartbeat
applies the same one.

`SIGHUP` is a forced reload: the document runs even when its bytes did not
move. That is how a license file replaced behind an unchanged `license` path
is picked up — a stat of the config file cannot see it, and a `SIGHUP` (or
any later rule edit) re-resolves the path and adopts the new document.

On Kubernetes, mount the ConfigMap as a volume, not through `subPath`: the
kubelet updates a volume in place (an atomic symlink swap the stat sees, on
its sync period of about a minute) and never updates a `subPath` file.

The `license` key is the lowest of the local sources. While `-license` or
`HOOP_LICENSE` holds the document in force, editing the key changes nothing
and the process says so once, the same answer a restart would give. Under a
control plane the file is not the owner: the heartbeat applies edits made
there, and `SIGHUP` logs that and does nothing.

### Connecting it to a Control Plane

A sidecar can fetch its whole configuration from a Hoop Control Plane instead
of carrying its own listeners. Two facts connect it, highest precedence first:

| Fact | Sources |
|---|---|
| URL | `HOOP_CONTROL_PLANE_URL`, then the `control_plane_url` config key |
| Credential | exactly one of: the token flag (`--token` / `-token`), then `HOOP_SIDECAR_TOKEN`; `HOOP_SIDECAR_IDENTITY_TOKEN_FILE`; `HOOP_SIDECAR_IDENTITY_GCP` |

First wins, not first valid, same as the license sources: an env var holding
garbage is an error, never a reason to fall through to the file. Two
credentials set is an error naming both, a credential with no URL is an
error, and a URL with no credential is an error naming all three.

```bash
# No config file at all: the env pair is the whole configuration.
HOOP_CONTROL_PLANE_URL=https://cp.example.com \
  hoop start sidecar --token hsc_...

# Or name the plane in the file and pass only the token.
echo 'control_plane_url: https://cp.example.com' > config.yaml
hoop start sidecar --config config.yaml --token hsc_...
```

The plane issues the token once, when you register the sidecar
(`POST /api/sidecars`), and stores only a hash of it, so losing the token
means registering a new sidecar. The token flag and `HOOP_SIDECAR_TOKEN`
hold the token itself, never a path, and no config key exists for it: a
bearer secret does not belong in a file that gets committed.

#### Service account identity instead of a token

A fleet does not have to register each sidecar. The sidecar can present an
identity the platform already issued (header `hoop-sidecar-identity`), and
the plane maps it to a sidecar through a service account allowlist entry
(issuer, audience, subject pattern, name template). The first handshake
creates the sidecar; restarts, new pod names and replicas land on the same
one, and a sidecar an admin deleted stays deleted.

| Variable | Holds |
|---|---|
| `HOOP_SIDECAR_IDENTITY_TOKEN_FILE` | path to a JWT, normally a Kubernetes projected service account token. Re-read on every request, because the kubelet rotates it in place. Whitespace is trimmed; an empty file or one over 16 KiB is refused |
| `HOOP_SIDECAR_IDENTITY_GCP` | `true` to fetch a Google ID token from the GCE/GKE metadata server, with `HOOP_SIDECAR_IDENTITY_AUDIENCE` as audience. Cached until 5 minutes before it expires. `GCE_METADATA_HOST` overrides the server, as in the Google client libraries |
| `HOOP_SIDECAR_IDENTITY_AUDIENCE` | the Google ID token's audience. Default: the control plane URL. Only `HOOP_SIDECAR_IDENTITY_GCP` reads it; set without `HOOP_SIDECAR_IDENTITY_GCP=true` (beside the token, `HOOP_SIDECAR_IDENTITY_TOKEN_FILE`, or alone), it stops startup, because a projected token's audience is set where the token is minted |

Kubernetes projected token, audience set to the control plane URL exactly as
`HOOP_CONTROL_PLANE_URL` holds it (or to the organization's own audience, see
below):

```yaml
spec:
  serviceAccountName: hoop-sidecar
  containers:
    - name: hoop-sidecar
      env:
        - name: HOOP_CONTROL_PLANE_URL
          value: https://cp.example.com
        - name: HOOP_SIDECAR_IDENTITY_TOKEN_FILE
          value: /var/run/secrets/hoop/token
      volumeMounts:
        - name: hoop-token
          mountPath: /var/run/secrets/hoop
          readOnly: true
  volumes:
    - name: hoop-token
      projected:
        sources:
          - serviceAccountToken:
              path: token
              audience: https://cp.example.com
              expirationSeconds: 3600
```

The allowlist entry for it uses the cluster's OIDC issuer, claim `sub`, and a
pattern such as `system:serviceaccount:*:hoop-sidecar` with a name template
such as `gke-eu-{1}`, so each workspace namespace becomes one sidecar.

On GKE with Workload Identity, bind the Kubernetes service account to a
Google service account and let the metadata server mint the token instead;
no volume is needed:

```yaml
      env:
        - name: HOOP_CONTROL_PLANE_URL
          value: https://cp.example.com
        - name: HOOP_SIDECAR_IDENTITY_GCP
          value: "true"
```

Its allowlist entry uses issuer `https://accounts.google.com`, claim `email`,
and the Google service account's address. A 401 at startup names the issuer
and subject the sidecar presented, so the entry can be checked against them;
a control plane that predates service account support answers 401 as well.

When the plane cannot verify the token (not a JWT, an issuer no entry names,
a bad signature, an expired token, or keys it could not fetch), the 401 says
only `the service account token failed verification`. The plane does not tell
an unverified caller why; the reason is in the control plane log, with the
issuer. A refusal after verification (a subject no pattern allows, a deleted
sidecar, a binding to another service account) keeps its own message.

##### One control plane, several organizations

The plane maps one (issuer, audience) pair to one organization. Sidecars of
two organizations that share a plane and an issuer (one GKE cluster, or
`https://accounts.google.com`) must present different audiences, or the
second organization cannot add its entry. Give each organization its own
audience: `HOOP_SIDECAR_IDENTITY_AUDIENCE` under GCP, the projected volume's
`audience` under Kubernetes (`controlPlane.identityAudience` in the helm
chart), and the same value in that organization's entry. One organization on
a plane leaves it unset and uses the plane URL.

##### A sidecar keeps the first service account that reached it

The first service account that reaches a sidecar binds to it. Replicas and
restarts present the same account, so they reach it too. A different
service account whose name template renders the same sidecar name is refused
with 401 until an admin clears the binding:

```bash
curl -X DELETE -H "Authorization: Bearer $HOOP_ADMIN_TOKEN" \
  https://cp.example.com/api/sidecars/<name>/identity
```

The next service account to reach it then binds to it. A sidecar registered
with a token has no binding; a service account reaches it only when its
entry sets `adopt_existing_sidecars: true`. The first one binds to it, and the
sidecar's token keeps working.

At startup the process runs the handshake
(`POST {url}/api/sidecars/handshake`). The plane answers with the document
stored for this sidecar, and that document becomes the running config,
checked by the same strict decoder the file path uses. `--config` becomes
optional. A plane holding no configuration answers 412, and the process
seeds it with the file's document (`PUT {url}/api/sidecars/configuration`),
then serves what the plane sends back: a sidecar that ran standalone
connects by adding the URL and passing the token, nothing else. The import
happens only into an empty plane; once the plane holds a configuration it
owns it, and listeners still in the file are ignored with a warning rather
than merged: two authorities for one fact is the same mistake as a field
written in two spellings. The pushed document drops `control_plane_url` and
`license`: the URL is connection metadata this process already resolved, and
the license belongs to the organization, which serves its own on every
handshake. A first handshake that fails stops
startup, since there is nothing to serve yet.

Once running, a heartbeat repeats the handshake about every minute (each
wait is drawn from 48 to 72 seconds, so a fleet started together does not
stay in step). It keeps the
plane's last-seen fresh and picks up edits, applying them under the same
boundary as a file edit: rule-only drift swaps in place, logged as
`configuration applied` with a generation number; drift beyond the rules
logs `restart to apply it`. A failed heartbeat changes nothing, because
losing the phone line home must not take the data path down with it.
ADR-0014 records the boundary.

#### Upgrading: the control plane first

Upgrade the control plane, then the sidecars. A sidecar refuses a whole
document that holds one key its build does not decode, so the plane must
know what each sidecar decodes. From 1.210.0 it does (ADR-0022):

- It serves only the fields an admin set. A plane upgrade alone changes
  nothing an older sidecar receives.
- A save that sets a field, a rule type or a protocol that a connected
  sidecar's build lacks answers 422, naming the sidecar and the upgrade it
  needs. A sidecar that never connected is checked at its first handshake,
  which answers 422 the same way.
- This covers every sidecar from 1.162.0, the first release that handshakes.

A sidecar newer than its plane runs, but cannot use a key the plane does not
know. Its first import into an empty plane fails when its file sets such a
key, and startup stops. Do not roll a plane back below a key a stored
document uses: the plane answers 500 on that sidecar's handshake.

When a sidecar refuses a document anyway, it exits at startup with an error
that names the key. On a heartbeat it keeps running its last rules and
reports `refused`. The sidecar page shows `refused`, the version and the
reason until a document applies; `not_applied` there is the shape of a boot
crash-loop.

**Escape hatch.** `PATCH /api/sidecars/<name>` with
`{"configuration": {"load_from_disk": true}}` hands the sidecar back to its
own config file. The plane then serves only that flag and the license, so the
sidecar's own build decodes everything it runs. The switch deletes the rules
imported from this sidecar that nothing else uses, and unbinds the rest.
`load_from_disk: false`, sent alone, returns ownership to the plane: the
stored document is cleared, and the sidecar imports its file again.

#### Sessions on the plane

A plane that records sidecar sessions says so on every handshake answer with
`hoop-sidecar-session-events: true` (the organization's
`experimental.sidecar_session_events` flag). The process then also sends its
audit events to `POST {url}/api/sidecars/events`, in batches of at most 500
events and 4 MiB, with the same redaction and statement cap as the audit file.
Each event carries `seq`, its number in its session from 1, so a resend is
safe: the plane ignores a seq it already applied. A 5xx or an unreachable
plane gets the same batch again, with backoff; a 4xx is final.

The audit file stays the record of truth. The send never blocks a statement
and never fails one: a queue of 16 MiB holds what the plane has not taken,
and when it is full the oldest events go first. `GET /stats` reports the
count under `session_events.dropped`. An answer without the header, or a
401, 403, 404, 405 or 412 to a batch, stops the sending and empties the
queue; the next handshake that answers the header starts it again. A plane
older than this build sends no header, so nothing is sent to it. A proxy that
answers 413 makes the batches smaller (ingress-nginx allows 1 MiB unless
`proxy-body-size` says more). The copy goes to the plane whatever the
`audit.file` setting is: an `audit.file` of `/dev/null` keeps nothing locally
and still sends.

### Usage analytics

A release build reports usage to Segment: that the process started, what
shape its config has, how much traffic it judged, and why it stopped. Counts
and shape only. No statement text, no identity, no rule name or pattern, no
prompt, no token, no listener or upstream address — the same line
`/config` draws, enforced in `sidecar/analytics` and `daemon/analytics.go`.

| Event | When | Carries |
|---|---|---|
| `hoop-sidecar-first-run` | a bare invocation served the default page | port, whether it fell back, how long it stayed up |
| `hoop-sidecar-started` | every lane built, about to serve | config source and format, license state and type, lane count per protocol, how many lanes enforce / observe / mask / consult OPA / run an analyzer, rule totals, PII entity count, audit sinks |
| `hoop-sidecar-config-applied` | an edit reached the reloader, from the control plane or the config file | generation, outcome (`applied`, `restart-required`, `refused`), which sections changed, lanes swapped and kept |
| `hoop-sidecar-usage` | every 15 minutes and at shutdown | connections and statements in the window, denied and masked counts, denials by evaluator kind, analyzer calls, failures and rate-limited statements, audit write failures, per protocol |
| `hoop-sidecar-stopped` | the process is exiting | reason (`signal`, `listener-failed`, `license-expired`), failure class, uptime |
| `hoop-sidecar-license-expired` | the term ended under a config the free tier refuses | rule totals that exceeded it, term length, warnings sent |

Every event also carries the version, the entry point (`hoop`,
`hoop-inspect` or `embedded`), OS and architecture, the `runtime`
(`linux`, `docker`, `kubernetes`, `macos`, `windows`), and two identities.
`sidecar-id` says which install: `HOOP_SIDECAR_ID` if you set one, else the
control plane token (or, for a service account identity, the plane URL plus
the identity's issuer and subject, never the rotating token), else the
hostname plus the config file path — stable
across restarts and config edits. `host-id` says which machine: `HOOP_HOST_ID`
if you set one, else the OS machine id plus the hostname. Every source is
hashed; nothing leaves the process in the clear.

What the process can learn on its own depends on where it runs, so set the
variable the table names and nothing else:

| Runs on | `sidecar-id` | `host-id` |
|---|---|---|
| Linux VM, bare metal, macOS | derived, nothing to set | derived from `/etc/machine-id` or `IOPlatformUUID` |
| Docker | hostname is the container id: pass `--hostname` or set `HOOP_SIDECAR_ID` | container's own; set `HOOP_HOST_ID` to group by machine |
| Kubernetes | hostname is the pod name: set `HOOP_SIDECAR_ID`, or connect a control plane | set `HOOP_HOST_ID` from `spec.nodeName` via the downward API |

A control plane credential makes `HOOP_SIDECAR_ID` unnecessary anywhere.

Switch it off with `HOOP_SIDECAR_ANALYTICS=off`. A binary built without the
write key (`go build` from this tree, the compose stack's image) sends
nothing either way; the startup log says `usage analytics enabled` when it
will.

## Configuring it: config.yaml

One file is the whole configuration. The process reads it at startup, resolves
every listener, and then tells you what it resolved. YAML and JSON both work
and the extension picks the parser: `.yaml` and `.yml` go through the nested
`config/yaml` module, anything else is read as JSON. Decoding is strict, so a
mistyped key fails the startup instead of silently disabling a control.

Run with no config at all — no file, no flags, no control plane — and a
built-in default starts instead of a usage error: one loopback HTTP
listener (port 15321, or a free one when that is taken) answering every
request with a redirect to the getting-started guide at
https://hoop.dev/docs. It is not a relay lane: no codec, no policy, no
audit. It exists so the first run after the install shows a working URL
and a next step, and the banner says exactly that. Any flag keeps the
usage error, and a config file replaces the default entirely.

### What this build limits, and the license that lifts it

Unlicensed, one guardrail rule and one data masking rule, for the whole
process. The count is what the file AUTHORS, not what each lane resolves: a
rule in the top-level `guardrails` block is one rule however many listeners
inherit it, and a lane that overrides `mask.rules` with `[]` spends nothing.
The analyzer is not a rule and is not capped — its controls are the trigger
and `max_calls` — and neither are any deprecated `ai_analysis` rules still in
a guardrails block.

A config over either cap is refused at startup, naming every block that
authored rules and how many each holds, so the message reads as a map of what
to merge:

```
hoop-inspect: invalid config:
  - 2 guardrail rules are configured (guardrails: 1, appdb: 1) and this
    process enforces at most 1; merge them into one rule. A license lifts this
    cap: add one with the license flag, the HOOP_LICENSE environment variable,
    or the "license" key in the config file. Contact our support at
    https://help.hoop.dev. ai_analysis rules are counted separately and are
    not limited
```

The free-tier numbers are constants in `sidecar/daemon/limits.go` rather than
config keys, because a cap the file it limits can raise is documentation. They
mirror the control plane's free tier, which caps the same two things per
organization.

A license Hoop signed lifts them, per feature. Where it comes from depends on
one thing: whether this process is connected to a control plane.

**With a control plane that manages licensing, it is the only source.** A
gateway older than this feature does not manage it and says so by staying
silent, and then the three local sources below rank as they always did — an
upgraded sidecar never loses its license to an older gateway.

The organization's license is
held once, on the organization, and written into the configuration document
the handshake answers with; nothing is stored per sidecar, and a `license` key
authored on a sidecar's configuration is refused. The sidecar verifies that
signature itself, so a license grants nothing because of who sent it. A plane
whose organization holds no license runs the free tier, and any license in the
three local sources below is ignored — the process says so in a warning at
startup. The fleet's license is the fleet's: a pod that could license itself
from its own environment would mean an admin removing a license in the control
plane had removed nothing.

**Standalone, the three local sources rank as they always have,** and the
first that holds anything decides:

| Source | Spelling |
|---|---|
| Command line | `hoop-inspect -license …`, `hoop start sidecar --license …` |
| Environment | `HOOP_LICENSE` |
| Config file | `license: …` |

The value is a path to the document Hoop issued, or the document itself: a
value starting with `{` is read as the license, anything else as a filename.
That is one field for a mounted secret and for a Helm value, so moving a
license between the two is not also a rename.

```yaml
license: /etc/hoop-inspect/license.json
```

First wins, not first valid. A `HOOP_LICENSE` that points at nothing is an
error rather than a reason to fall through to the config file, because a
process that quietly ignored your environment variable will surprise you on
the restart after the file changes.

A license renewed in the control plane reaches the process on the next
heartbeat, with no restart: the caps move with it and the new term is logged.
A license REMOVED there reaches it the same way, and drops the process to the
free tier rather than falling back to a local source — the startup path
answers identically, so a restart never relicenses what a heartbeat
unlicensed. When the running rules no longer fit under the license that
arrives, the relay stops rather than serving them: it drains, exits, and the
supervisor restarts it, where the config is refused by name until somebody
renews the license or removes rules. Rules are never dropped from a live proxy
to fit a smaller license, for the same reason an ended term stops the process
instead of re-applying the caps. A control plane that becomes unreachable changes nothing: the
license it last sent keeps serving. A document that fails verification is
logged and dropped, and the license already in use keeps serving. A license that GRANTS LESS than the running
config needs is the one case a heartbeat cannot apply — the reload is
refused and the running rules keep serving under the license they were built
with until the process restarts, for the same reason an expiring term stops
the relay instead of deleting rules from it.

The license names the features it covers, and each one lifts its own cap:
`guardrails` and `data-masking` are the two this process reads. A license
naming neither field covers everything; one naming only `data-masking` leaves
the guardrail cap exactly where it was. An `oss` license verifies and grants
nothing, which is what the control plane does with the same value.

What the process concluded is the first line of its startup output, and
`-validate` prints it too:

```
license: valid. enterprise "Acme Corp", expires 2027-01-30, features: all (from HOOP_LICENSE)
license: expired. "Acme Corp" expired on 2026-05-01, running the free tier. Renew it at https://help.hoop.dev (from the license flag)
license: missing, running the free tier. Add one with the license flag, the HOOP_LICENSE environment variable, or the "license" key in the config file
```

Missing and expired are states, not failures: the process starts, the caps
stay in force, and an expired one logs at WARN so the reason a config that
loaded last month stops loading is the first thing in the log. A license that
cannot be READ is different and stops startup, naming the source and what a
good value looks like: a process that drops to the free tier over a typo in
a path is the silent downgrade this build refuses everywhere else.

`/config` serves the same verdict under `license`, without the signature, and
the caps under `limits`, where a `null` means the license lifted one.

#### When a term ends under a running process

The verdict is a function of the clock, not a flag set at startup. A relay
that has been up for months re-reads its own term, so `/config` and the caps
flip to the free tier the second the license lapses. Nothing has to reload.

What happens next depends on whether the config needs the license:

- **Inside the free tier**, nothing. The process keeps serving, reports
  `expired`, and an operator renews whenever they get to it. Expiry took
  nothing away, so taking the relay down would be an outage with no revenue
  behind it.
- **Over the free tier**, the relay stops. It logs the expiry, drains open
  connections, flushes the audit trail and exits non-zero. The supervisor
  restarts it, and startup then refuses the config by name until somebody
  renews or removes rules.

The stop is deliberate and the alternative is worse. Lanes are built once and
hold their rules for the life of the process, so re-applying the caps to a
running relay would mean deleting rules: dropping a guardrail lets through
statements it was refusing, and dropping a mask rule leaks the values it was
hiding. A billing event must never widen what a proxy allows.

To keep it from being a surprise, the log counts down once a day through the
last fortnight of a term:

```
WARN license expires soon, and this config needs it: the relay stops when the term ends days_left=6 expires=2026-05-01T00:00:00Z renew=https://help.hoop.dev
WARN license: expired. "Acme Corp" expired on 2026-05-01, running the free tier. Renew it at https://help.hoop.dev
WARN stopping: the license term ended and this config needs more rules than the free tier allows free_tier="1 guardrail rule(s), 1 data masking rule(s)"
```

The clock is polled once a minute rather than slept on with one long timer,
so an NTP correction or a host that suspended through the expiry is still
caught. Expect the stop within a minute of the term ending.

The verifier is `sidecar/license`, a standard-library reimplementation of
`common/license`: same key, same signature, same JSON, because this module
cannot import the gateway's without inheriting its dependency tree. The two
are pinned together by `client/licensecompat`, the only module that can see
both. `allowed_hosts` is the one thing the sidecar does not check, since the
address it could offer is a scheduler-generated pod name.

A verdict cannot be handed to the daemon, only earned. The flag that says a
license verified is unexported and `license.Load` is the only thing that sets
it, after checking the signature, so a `license.Status` built by hand reports
`invalid` and lifts nothing. `Config.UseLicense` takes a reference rather than
a verdict for the same reason: when the control plane starts sending licenses,
it will send the document Hoop signed and the sidecar will check that
signature itself, instead of trusting whoever is on the connection.

That leaves a test no way to run a licensed daemon, which
`sidecar/license/licensetest` fixes honestly. It generates a keypair, points
the trust root at it for the duration of one `*testing.T`, and signs documents
that go through the same `Load`. Every function there takes a `*testing.T`,
which is the guard: production code calling one would have to import
`testing`.

### Deprecated fields

0.1.0 renamed six keys, removed one, and reversed two defaults. Both
spellings load for two minor releases and the old one prints a warning.
Setting both spellings of one field at one scope refuses the config, because
picking a winner silently would contradict the rule the decoder already
enforces: a typo in a key must not disable a control. Read this before the
example below. Every config written against the old reference needs a pass.

| Old | New | While deprecated |
|---|---|---|
| `policy.rules` | `guardrails.rules` | works, warns |
| `policy.enforce: true\|false` | `guardrails.mode: enforce\|observe` | works, warns |
| `policy.opa.*` | `opa.*` | works, warns |
| `mask.rules[].entity: X` | `mask.rules[].entities: [X]` | works, warns |
| `mask.enabled: true` | drop the key; rules present means masking is on | works, warns |
| `mask.enabled: false` with rules | drop the rules from that scope | stays authoritative, masking stays off, warns |
| `listeners[].connection` | `listeners[].name` | maps onto `name` when `name` is empty, warns |
| `audit.fail_closed: <x>` | `audit.fail_open: <the opposite of x>` | works, warns |
| `pii` omitted | still omitted | the detector gains all 54 entity types |
| `guardrails.rules[].type: ai_analysis` | the listener's own `analyzer` block | works, warns; see [Migrating from `type: ai_analysis` rules](#migrating-from-type-ai_analysis-rules) |

Three of those move traffic on the upgrade, and each one deserves a read
before the restart:

- **A config that never set `enforce` starts denying.** `guardrails.mode`
  defaults to `enforce`, so a rule set nobody validated because it never fired
  now fires. Run `-validate` first and read the resolved rule count per lane.
- **A config that never set `audit.fail_closed` starts refusing statements
  whose audit write failed.** `audit.fail_open` defaults to `false`. The two
  keys are negations of each other, so the migration reads the old field
  rather than pattern-matching the new one: a config that wrote
  `fail_closed: false` resolves to `fail_open: true` and keeps the behaviour
  it had. Only a config that omitted the field changes.
- **Mask rules on a protocol that cannot mask stop loading.** `mask.enabled`
  used to gate that check, so a lane omitting the flag skipped it and booted
  with rules that could never fire. The check now runs on `mask.rules` alone,
  and such a lane refuses to start.

`entity` renames the same way beside `columns`, and that pairing is worth a
second look because the two keys do different jobs on one rule. The columns
decide which cells are masked; the entity only names them in the audit trail.
So the list holds at most one entry there:

```yaml
- {name: taxpayer, entity: US_SSN, columns: [taxpayer_id]}      # old
- {name: taxpayer, entities: [US_SSN], columns: [taxpayer_id]}  # new, same rows
```

Both mask every `taxpayer_id` cell whatever it holds, and both record the
masked cells as `US_SSN`. Two entities beside `columns` is refused: one cell is
audited under one name and nothing can pick between two. Name none and the
cells are reported as `column:taxpayer_id` instead. The label is not checked
against `pii.entities`, because a column rule runs no detector — it is a name
for an audit row, not a class a recognizer has to produce.

Omitting `pii` reverses direction too, in the safe one. The section used to be
required before anything could detect, and it now subtracts from a detector
that already holds every entity type. Nothing that worked stops working.

`hoop-inspect -validate -strict` exits non-zero on any deprecation, so a
pipeline can fail on the old spelling before the release that removes it does.

`hoop-inspect -migrate` rewrites the file for you:

```bash
hoop-inspect -migrate -config config.yaml -migrate-out config-new.yaml
```

It loads the config through the same strict decoder the daemon uses, folds
every renamed field above onto its replacement, moves `type: ai_analysis`
rules onto listener `analyzer` blocks where the move is faithful (see
"Migrating from `type: ai_analysis` rules" below), and emits a document that
passes `-validate -strict`. The report on stderr names everything it moved
and everything it left for you: a lane carrying two ai rules, or a top-level
rule some lane cannot absorb, keeps the rule form with a note instead of a
guess. The output's extension picks the syntax (stdout inherits the input's),
and comments and key order from the original are not preserved — review the
diff before deploying. `hoop start sidecar --migrate` is the same command.

### 1. Write the file

Top-level `policy` and `mask` are DEFAULTS. Each listener is one upstream, and
inherits those defaults unless it overrides them.

```yaml
log_level: info

# A path to the license Hoop issued, or the document itself. The license flag
# and HOOP_LICENSE both outrank this. Omit it to run the free tier.
license: /etc/hoop-inspect/license.json

# The Control Plane this sidecar connects to. HOOP_CONTROL_PLANE_URL outranks
# this. When set, the handshake supplies the whole running config and this
# file must not also declare listeners; omit it to run standalone from this
# file alone.
# control_plane_url: https://cp.example.com

admin:
  listen: 127.0.0.1:19000   # /healthz /stats /config /events /api/*

# Review status for agents; needs a control plane. See "Agents over MCP".
# mcp:
#   listen: 127.0.0.1:8765

audit:
  file: "-"                 # stdout as JSON lines; a path appends to that file
  async_queue_size: 1024    # a slow sink must not block a user's query
  memory_buffer: 256        # last N events, readable at GET /events
  query_sessions: 500       # backs GET /api/sessions
  fail_open: false          # the default: a statement whose audit write failed is refused

pii:                        # omit the section and all 54 entity types are active
  entities: [EMAIL_ADDRESS, US_SSN, CREDIT_CARD, BR_CPF, IBAN_CODE]

guardrails:                 # Hoop's own rules, inherited by every listener
  mode: enforce             # the default; observe evaluates and records, denying nothing
  rules:
    - name: no-cpf-in-query
      type: pii
      entities: [BR_CPF]
      message: do not put a taxpayer id in a query; it lands in the database's own logs

mask:                       # inherited by every listener
  rules:                    # a non-empty list is the whole switch
    - {name: emails, entities: [EMAIL_ADDRESS], strategy: redact}
    - {name: ssn, entities: [US_SSN], strategy: partial, keep_last: 4}

listeners:
  - name: appdb             # audit rows and input.context.connection key on this
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
    guardrails:
      rules:
        - name: no-destructive-sql
          type: operation   # classifier-derived, so SELECT 'DROP TABLE x' is a select
          operations: [drop, delete, truncate]
          message: destructive statements are not permitted on appdb
    mask:
      rules:                # REPLACES the default list rather than extending it
        - {name: ssn-column, entities: [US_SSN], columns: [ssn], strategy: partial, keep_last: 4}
        - {name: emails, entities: [EMAIL_ADDRESS], strategy: redact}

  - name: httpbin
    protocol: http
    listen: 0.0.0.0:18080
    upstream: httpbin:8080
    opa:                    # someone else's Rego, its own section beside guardrails
      url: http://opa:8181/v1/data/hoop/inspect
      fail_open: false
    guardrails:
      rules:
        - name: no-admin-api
          type: http_resource
          resources: ["/admin/**"]
          message: the admin API is not reachable through this proxy
        - name: no-upstream-5xx
          type: http_status   # response-side, so ext_authz cannot ask it
          statuses: ["5xx"]
          message: upstream failure suppressed by policy
```

That file is over both caps and this build refuses it as written: four
guardrail rules and four mask rules, where one of each is allowed. It stays
whole because a config holding one rule per section cannot show a listener
overriding a default at all, and inheritance is what the section is teaching.
The stack config in `deploy/docker-compose/envoy-stack/sidecar/config.yaml` is
the version that loads: same shape, the rest commented out and marked. See
[What this build limits, and the license that lifts
it](#what-this-build-limits-and-the-license-that-lifts-it).

Rule types and what each one matches are in [Guardrails and
OPA](#guardrails-and-opa); masking strategies and the entity-versus-column
choice are in [Masking and PII](#masking-and-pii).

`audit.fail_open` replaced `audit.fail_closed` and inverted with the rename, so
`fail_closed: false` and `fail_open: false` mean opposite things and only a
config that wrote neither changes behaviour.

Other listener fields worth knowing: `network: unix` binds a filesystem socket
instead of a port (see [Transport](#transport)), `upstream_tls` encrypts the
connection to the backend (see [Upstream TLS](#upstream-tls)),
`idle_timeout_sec` closes an idle connection (leave it unset for interactive
sessions, since psql idles between keystrokes), and `max_conns` bounds
concurrency.

`identity_header` names the request header an authenticating proxy in front
sets from a verified credential, and is valid on `http`, `grpc` and `spanner`
lanes only. An http lane reads it on EVERY request, because Envoy pools
upstream connections and sends requests from different callers down one
keep-alive connection. A request naming a different principal ends the
session (`session_end`) and opens a new one on the same connection
(`session_start`), so the policy context and the audit rows of each request
carry its own caller. A caller change while an earlier response on the
connection is still outstanding is refused: that response belongs to the
session that sent its request. The header value is lifted out of the
statement, so it reaches policy input and the audit trail only when
`http.headers` names it. A grpc lane reads it per RPC. The header is trusted
exactly as far as the network is: nothing but that proxy may be able to reach
the listener.

**`google_identity: {}` names the caller from Google's own bearer**, for an
http lane with no authenticating proxy in front. The lane verifies the
request's `Authorization: Bearer` token (kubectl through GKE Connect Gateway
sends a Google OAuth2 access token) with a POST to
`https://oauth2.googleapis.com/tokeninfo`, never a query string, because a
URL is what every proxy on the path logs. The principal is the verified email,
else the account's `sub`. A verified answer is cached under the token's
SHA-256 for the token's lifetime, at most five minutes; a rejected token for
30 seconds; an unreachable Google never. Any failure (no bearer, a token Google
rejects, Google unreachable) refuses that request with a 403, so a caller that
cannot be verified is never served under the identity the connection had
before. The token reaches no audit row, no policy input and no analyzer
prompt. `http` lanes only, and exclusive with `identity_header`.
`tokeninfo_url` overrides the endpoint for Private Service Connect or a
`private.googleapis.com` VIP and must be `https`. The result names who holds
the token, not what it may do: Connect Gateway still checks the same token
against IAM and RBAC. The example is in [kubectl through GKE Connect
Gateway](#kubectl-through-gke-connect-gateway).

### 2. Know how a listener inherits

| Field | Merge | Why |
|---|---|---|
| `guardrails.rules` | concatenate, listener first | Every local rule denies or defers and the first match wins, so concatenating is monotonic in the allow/deny outcome. Order picks which name and message the user reads. |
| `guardrails.mode` | replace when set | A lane rolling out behind an enforcing default has to be able to say observe. |
| `opa` | replace when set | One lane has one decision endpoint. An explicitly empty `opa: {}` on a listener drops an inherited one. |
| `mask.rules` | replace when set | A rule owns an entity or a column, and two concatenated lists leave two rewrites of one value. `mask.rules: []` is how a lane opts out of an inherited set. |
| `analyzer` (listener block) | listener-only; fields inherit the top-level `analyzer` defaults | The trigger, risk actions and prompt are per lane; the provider, model and credential stay in the top-level section, whose `send`/`fail_open`/`timeout_sec`/`max_input_bytes`/`max_calls`/`cache` values the block overrides field by field. |
| `pii`, `analyzer` (top level), `audit`, `admin`, `trust` | process-wide | One detector engine, one provider and one trust pool per process. |

### 3. Validate before you deploy

Nothing needs to be running. Run it against the Envoy stack's own config, from
the `sidecar` directory:

```bash
cd cmd && go build -o hoop-inspect . && \
  ./hoop-inspect -validate -config ../../deploy/docker-compose/envoy-stack/sidecar/config.yaml
```

```
config OK: 2 listener(s)
  license: missing, running the free tier. Add one with the license flag, the HOOP_LICENSE environment variable, or the "license" key in the config file
  limits: 1 guardrail rule(s), 1 data masking rule(s)
  appdb            postgres  enforcing 1 rule(s) + masking
  httpbin          http      enforcing 1 rule(s) + masking
```

Each line is the RESOLVED lane, so the counts include what it inherited. A lane
with an `opa` block reads `+ opa`, and one with `guardrails.mode: observe`
reads `observing N rule(s), denying none` with a note under it. Observe is a
dry run rather than an off switch: the lane builds the same chain, evaluates
every rule, allows the statement, and files the rule that would have refused
it under `guardrails.would_deny`. A lane that should skip the work sets
`guardrails: {rules: []}`.

`-validate` also prints a note where a resolved lane behaves in a way the file
does not show. A rule carrying `action: defer`, or an analyzer block deferring
a risk level, on a lane with no `opa.url` gets one, because that match or
level denies rather than reporting a finding. An analyzer with no trigger on
an ungated lane gets one too, naming the model call per statement shape it
implies.

Validation builds every lane, so it catches what a syntax check cannot, and it
reports every problem in one run rather than one per restart. It refuses these
outright:

- More guardrail rules or more mask rules than this process allows, naming
  every block that authored one. See [What this build limits, and the license
  that lifts it](#what-this-build-limits-and-the-license-that-lifts-it).
- A license that cannot be read or was not issued by Hoop, naming the source
  it came from. An expired one is a warning instead: the caps come back and
  the process runs.
- `mask.rules` on a protocol whose codec can carry neither masking mechanism,
  naming the lane. This check used to sit behind `mask.enabled`, so a lane
  omitting the flag skipped it and loaded rules that could never fire.
- Both spellings of one renamed field at one scope, naming the field. See
  [Deprecated fields](#deprecated-fields).
- A key typo, in YAML or JSON.
- A bad regex in any lane's rules, naming the lane.
- An analyzer (a listener's block, or a deprecated `ai_analysis` rule) with
  no top-level `analyzer` section, or with no action for any risk level.
  Both would load and classify nothing. An OMITTED trigger is legal and
  classifies everything on an ungated lane — a note, not a refusal, because
  declaring the analyzer is already the opt-in — and `-validate` prints the
  per-statement cost it implies.
- An analyzer on a lane whose protocol has no content builder, naming the
  protocol. That lane classifies nothing and says nothing while doing it: the
  analyzer returns before it has a status, so there is no finding and no
  annotation to notice.
- An `analyzer.provider` the binary does not link, naming what it does link.
- A credential file readable by group or other, naming its mode.
- An `http` block on a non-HTTP lane, or `authorization` in its header
  allowlist.

### 4. Ask the running process what it resolved

Reading the file cannot tell you which rules a lane ended up with, because
inheritance happens at startup. Debugging a denial that never fired starts
here, again against the stack config:

```bash
curl -s localhost:19000/config | python3 -m json.tool
```

```json
{"lanes": [
  {"name": "appdb", "protocol": "postgres", "enforcing": true,
   "rules": ["no-cpf-in-query"], "masking": true},
  {"name": "httpbin", "protocol": "http", "enforcing": true,
   "rules": ["no-cpf-in-query"], "masking": true}
 ],
 "license": {"state": "missing"},
 "limits": {"guardrail_rules": 1, "mask_rules": 1}}
```

Both lanes resolved the same rule, because the process's one guardrail rule is
a top-level default and neither lane authors its own. Rule names only: a
`pattern_regex` can encode business logic, and this endpoint already sits
beside a read interface to the audit trail. `limits` is what this process
refuses to exceed, served here so an operator asking why a second rule will
not load reads the answer from the endpoint that told them what did, and a
`null` there means a license lifted that cap. `license` is the verdict behind
those numbers: the type, the customer, the term, the features and the source,
never the signature.

### Sharing a rule block between lanes

This is the reason to prefer YAML. Anchors let several listeners reference one
block, and a top-level key starting `x-` is dropped before validation, so an
anchor does not need a matching config field.

```yaml
x-readonly: &readonly
  - name: no-writes
    type: operation
    operations: [insert, update, delete, drop, truncate]
    message: this credential is read-only

listeners:
  - {name: replica-a, protocol: postgres, listen: 0.0.0.0:15432, upstream: a:5432, guardrails: {rules: *readonly}}
  - {name: replica-b, protocol: postgres, listen: 0.0.0.0:15433, upstream: b:5432, guardrails: {rules: *readonly}}
```

`guardrails.mode` defaults to `enforce`, so both lanes deny on the first
deploy with no third key to remember. The rollout path is `mode: observe`,
which evaluates the same chain, allows the statement anyway, and records the
rule that would have refused it. The audit line stays `kind: statement` with
`allowed: true`, carries the rule name and message, and adds one annotation:

```json
{"kind":"statement","allowed":true,"connection":"reporting",
 "principal":"ana@corp","operation":"update","tables":["customers"],
 "rule":"customers-is-crm-owned",
 "message":"customers is owned by the CRM; write through it",
 "metadata":{"guardrails.would_deny":"customers-is-crm-owned"}}
```

A week of that answers the question the mode exists for:

```bash
jq -r 'select(.metadata["guardrails.would_deny"]) | .metadata["guardrails.would_deny"]' \
  audit.jsonl | sort | uniq -c | sort -rn
    412 customers-is-crm-owned
      7 no-unbounded-delete
      1 no-schema-changes
```

A dry run costs what enforcement costs, because nothing can report what would
have been denied without evaluating it. An observe lane with an analyzer
makes model calls and one with OPA makes round trips. Set
`guardrails: {rules: []}` on a lane that wants the cheap off switch instead.
Observe switches off nothing else: `ErrStreamUnsafe` still denies, a failed
audit write still denies while `audit.fail_open` is false, and startup
validation runs in full. A lane in observe says so in the startup log and in
`/config`.

### Analyzing statements with a model

The AI analyzer is a per-listener component, declared beside `guardrails`,
`opa` and `mask` rather than inside them. It sends a statement to a language
model and denies on the risk it reports. It is the only evaluator that leaves
the process, costs money and can be slow, so three things bound it: a trigger
decides what is worth asking about, a cache collapses repeated statement
shapes onto one verdict, and it runs LAST in the chain, after the free local
rules and OPA.

Two config blocks share the work. The top-level `analyzer` section holds what
is genuinely process-wide — the provider, the model, the credential — plus
the defaults every lane inherits. Each listener's own `analyzer` block holds
what is per lane: the trigger, the risk→action map, the prompt, and any
default it wants to override.

The analyzer runs wherever a content builder renders the statement for a
model: `postgres`, `mysql` and `mssql` send the statement text with the
operation, tables and database the codec derived; `http` sends the method,
normalized resource and body. A lane whose protocol has no builder is refused
at startup rather than left classifying nothing, which is what a relay-only
protocol would otherwise get: no statements to render means no verdict,
silently.

```yaml
pii:                           # optional; omitting it activates all 54 types
  entities: [EMAIL_ADDRESS, US_SSN, BR_CPF]

analyzer:                      # one provider serves every lane; the rest are
  provider: vertex             #   defaults each listener inherits
  model: claude-sonnet-4-5@20250929
  extra: {project: my-gcp-project, region: global}
  # credentials_file omitted -> Application Default Credentials.
  # Under GKE Workload Identity there is then no credential on disk at all.
  timeout_sec: 10
  fail_open: true              # the default, and see below
  send: redacted               # raw | redacted | refuse
  max_input_bytes: 8192
  cache: {size: 4096, ttl_sec: 900}
  max_calls: 500               # lifetime ceiling; resets only on restart
  rate_limit:                  # how fast; refills, so a spike passes
    calls: 30                  #   30 calls...
    per_sec: 60                #   ...per 60 s
    burst: 30                  #   optional, defaults to calls

listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
    analyzer:                  # this lane's analyzer, beside guardrails
      trigger: {operations: [delete, update]}
      high: block
      medium: warn
      low: allow
      message: refused by risk analysis

  - name: api
    protocol: http
    listen: 0.0.0.0:18080
    upstream: httpbin:8080
    http:                      # what the codec exposes; see below
      capture_body: true
      max_body_bytes: 8192
      headers: [Content-Type]
      sensitive_query_params: [ticket]   # widens the codec's credential denylist
    analyzer:
      trigger: {resources: ["/anything", "/users/*/orders"]}
      high: block
      send: refuse             # overrides the inherited redacted, this lane only
      rate_limit: {calls: 10}  # per_sec and burst still inherited
```

**A trigger can AND its conditions.** The flat lists OR every value, so
"patch, under `/api` only" needs an item (ADR-0030):

```yaml
    analyzer:
      trigger:
        operations: [delete]            # flat lists: each value ORed, as before
        any:                            # an item matches when ALL its fields match
          - {operations: [patch], resources: ["/api/**"]}
        exclude:                        # an item here removes a match
          - {resources: ["/api/healthz"]}
```

A statement is classified when a flat list or an `any` item matches it and
no `exclude` item does. Fields left out of an item are not checked. An item
that names no field, or an operation no codec reports, is refused at startup. With no positive condition,
an ungated lane classifies everything except what `exclude` names. Under
`opa.gate` a gate `request` still replaces the whole trigger, `exclude`
included, and `exclude` with nothing else to narrow is refused. A control
plane serves `any` and `exclude` only to a sidecar that reports
`analyzer_trigger_items`.

**What the block may override.** `send`, `fail_open`, `timeout_sec`,
`max_input_bytes`, `max_calls`, `rate_limit` and `cache` all default to the
top-level value and replace it when the block names them; `rate_limit` and
`cache` merge field by field. `max_calls` and `rate_limit` on a block bound
that LANE's spend, and both key on the listener name, so a hot reload that
edits the block continues the running count and the running bucket rather
than re-arming them.
Provider, model, endpoint and credential are not per lane: a second provider
per lane would double the credential surface, so those stay in the top-level
section.

**`max_calls` and `rate_limit` are two different bounds.** A call must pass
both, and a cache hit spends neither.

- `max_calls` caps the TOTAL since the process started. It never refills: once
  spent, the lane reports `budget_exhausted` until a restart.
- `rate_limit` caps the SPEED. It is a token bucket: `burst` calls may go out
  back to back, then one every `per_sec / calls` seconds. An empty bucket
  reports `rate_limited`, and the lane classifies again as it refills. A call
  the rate refuses does not spend `max_calls`.
- When both are spent, the status is `budget_exhausted`, the one that lasts.
- `rate_limit` needs `calls` and `per_sec` together, after inheritance.
  `burst` sets how much of a spike passes: a migration that sends 500
  DELETEs in 10 s is throttled after the first `burst`.

`-validate` prints what each lane may spend in any 24 h at its rate, and how
long `max_calls` lasts at the full rate. A `max_calls` at or below `burst`
means the rate never fires, and the note says so. The process log gets one
warning when a lane's bucket first runs empty and one line with the refused
count when it grants again. The usage event counts throttled statements as
`analyzer-rate-limited`.

A sidecar older than 1.198.0 cannot decode `rate_limit`, so the control plane
refuses to serve a document that carries it to one.

**A request with no body is still classified.** On a REST API the path is
the operation: `GET /api/v1/namespaces/prod/secrets/db-root` says what a
kubectl user is about to read without a byte of body. The headers the lane
allowlists under `http.headers` go into the prompt beside it, because they
carry what the path does not — kubectl asks for a listing with
`Accept: application/json;as=Table;...` and for the contents with
`Accept: application/json`, on the same GET. That allowlist is the one
decision on what leaves the process for policy, the audit trail and, here,
a provider; `authorization`, `cookie`, `proxy-authorization` and
`set-cookie` cannot be allowlisted at all. `capture_body` is optional:
`true` puts a POST's payload in front of the model; without it a write is
judged from its request line alone. A lane where a level asks for
`require_review` captures request bodies without it (see Reviews). A
bodiless RESPONSE (a 204, a 101) is never classified: there is nothing to
judge but a status line.

**What the model sees** is the request line as the client sent it — verb,
raw path and query string — then the normalized resource where it differs,
the content type, the allowlisted headers in name order, and the body:

```
POST /users/12345/orders?export=all
Resource: /users/*/orders
Content-Type: application/json
X-Hoop-User: alice

{"format": "csv"}
```

Through GKE Connect Gateway the `Resource:` line is the Kubernetes path with
the cluster prefix removed, while the request line keeps it (see [kubectl
through GKE Connect Gateway](#kubectl-through-gke-connect-gateway)).

The raw target is deliberate, as the client spelled it. The resource is the
policy key, where every id must fold into one rule; the model is judging
intent, and `?export=all` or a `?limit=100000` is part of it. Identifiers in
the path are covered by the same `send: redacted` pass as identifiers in the
body. The CACHE keys on the resource plus the whole query and the headers,
names and values, so `/users/1` and `/users/2` with the same body cost one
call, not one per id, while `?dry_run=true` and `?dry_run=false` are two
verdicts, and so are the two `Accept` values above. Allowlisting a
per-request header (a session id) therefore costs a call per request; the
`-validate` per-statement note is the reminder.

**A credential in the query string never leaves the codec.** The value of
`access_token`, `api_key`, `key`, `sig`, `X-Amz-Signature` and the other
names in `codec/http.DefaultSensitiveQueryParams` is replaced by `[redacted]`
in the request line, `http.query` and `http.target` before a statement is
built, so the audit trail, OPA and the analyzer all see the same marker. The
key and its position survive: the record still says a token was passed. It is
a denylist; `sensitive_query_params` adds a deployment's own names and
nothing removes one.

**Only requests are classified.** By the time a response comes back a write
has already happened, and read-side exposure is masking's job.

**`fail_open` defaults to true here**, the opposite of every other evaluator.
A classifier that denies whenever its provider has an outage takes the
database down with it. Set it false where the classification is a compliance
requirement, and accept that a provider outage then stops traffic.

**`send: redacted` rewrites every detected value as its entity class** before
the statement leaves the process. A relay whose job is keeping taxpayer ids
out of a database's query log should not post them to a model vendor, and a
model judging `pan = '<CREDIT_CARD>'` classifies the same as one shown the
number. `send: refuse` denies locally instead of transmitting. Neither one
needs a `pii` section any
more: a detector always exists once the plugin is linked, and the section only
narrows which entity types it scans for.

**Handing the decision to OPA.** `high: block` is a level→action table with
three rows, and it sees only the level. Set the action to `defer` where the
real determination also needs the user, the hour or a break-glass list: the
analyzer classifies and records the level, and the block/allow choice moves
into Rego.

```yaml
opa:
  url: http://opa:8181/v1/data/hoop/inspect/result
  fail_open: false
  gate: true

listeners:
  - name: appdb
    # ...
    analyzer:
      # no trigger: with the gate on, Rego decides what is worth classifying
      high: defer
      medium: defer
      low: defer
```

The level then travels to OPA as `input.findings.ai_analysis`, one entry in a
map every producer on the lane reports into. `defer` is not analyzer-only:
any rule type takes `action: defer` and reports the same way (see [Reporting
instead of denying](#reporting-instead-of-denying)), so one statement can
hand Rego a risk level and a set of PII entity classes at once.

`defer` reorders the chain. A decision that reads the risk level has to run
after the thing that fills it, so OPA moves from before the analyzer to after
it, and a statement Rego would have refused for free now costs a model call
first. `gate: true` buys that back by consulting OPA on both sides: once
before the analyzer to answer "is this worth a model call", once after to
answer what the level means. Both calls hit the same URL and carry
`input.phase`, so a policy that ignores the field answers both identically,
and turning the gate on costs one round trip rather than a rewrite.

Two configs are refused at startup. `gate: true` on a lane with no analyzer
is a round trip that buys nothing. A block naming no action for any risk
level is refused too, because every verdict would then allow while looking
like enforcement. An omitted `trigger` is NOT a refusal: on a plain lane it
classifies everything (a model call per statement shape, bounded by the
cache and `max_calls`, and `-validate` says so in a note), and under
`gate: true` it is how you say Rego decides.

`defer` on a lane with no `opa.url` used to be a refusal too. It now
loads, warns at startup, and DENIES on a match. Deferring to a decision that
does not exist has to fail closed somewhere, and moving that from startup to
runtime lets one file serve a deployment with OPA and a deployment without
one. See [Guardrails and OPA](#guardrails-and-opa) for what the two phases
send and what the gate may answer.

**Holding a statement for a human.** The fourth action is `require_review`:
the statement is refused until a person approves it.

```yaml
listeners:
  - name: payments
    # ...
    analyzer:
      trigger: {operations: [delete, update]}
      high: require_review
      approval_rule: payments-approvers
```

`approval_rule` names the control plane access request rule that decides who
may approve. The rule holds the reviewer groups, the approval count and the
force-approval list; the lane holds only its name, and the control plane
authorizes each review against the config it stored for that sidecar.

**The review runs last.** The lane files the review only after every other
evaluator allowed the statement, the decide-phase OPA call included
(ADR-0030). A decide denial files nothing, pages nobody and spends no
approval. Decide sees the pending review as `input.review` (see [Guardrails
and OPA](#guardrails-and-opa)), and it can deny the statement but cannot
skip the review. A library caller that runs `analyzer.Evaluator` outside a
`policy.Chain` holds at once, because nothing runs after it.

**A hold waits, then gives up (ADR-0028).** A pending review holds the statement on
its connection for up to 30 minutes, on every protocol. Every 5 seconds the relay asks the plane
about that one review (`POST /api/sidecars/reviews/<id>/claim`); the ask never
files a review. An approval that lands in time runs the statement on the same
connection, late. A rejection, a revocation, or an approval another connection
already used ends the wait at once and denies. The review id is in every
denial:

```
ERROR:  statement held for human approval: still waiting for approval after 30m0s; run the statement again once it is approved (review 9f97…)
```

The wait ends early when the connection does. A client that hangs up, or an
upstream that closes, stops the polling before anything is claimed, so the
approval stays for the retry. The audit record carries the cause. Ctrl-C in
psql or mysql does NOT end it: both send the cancel on a separate connection,
where no statement runs. Close the client instead. An `idle_timeout_sec`
shorter than 30 minutes also ends the wait, since a held connection is idle;
`-validate` notes it. The caller's own deadline is the budget in practice: an
http client behind Envoy gives up after the 15s default route timeout.

After a timeout the developer asks an approver, then runs the statement again.
That retry collects the approval. The relay files nothing on the second
attempt: the plane recognizes the same statement, consumes the approved review
and answers that this one may go through. It answers that ONCE, since the
third run of the same statement files a fresh review. A rejection or a
revocation ends that one review: running the statement again files a new one.

**Revoking an approval.** An approved review can be revoked until the sidecar
uses it, from the review page or with `PUT /api/reviews/<id>` and status
`REVOKED`. A hold that is still waiting denies on its next poll, and the
Slack message says the approval was revoked. Once the sidecar uses the
approval, the review is `EXECUTED` and a revoke answers 400: the statement
already ran. Any decision that loses that race to the sidecar answers 400
the same way.

The budget and interval are constants, with no config field. A control plane
older than the relay has no claim route: the relay then denies after the first
poll, as it did before it could wait.

**`review_mode: return` denies at once.** It files the review and denies
without waiting, so an agent whose tool call ends in seconds gets the review
id instead of a hang (ADR-0021). The client resends the identical statement
after approval.

```yaml
    analyzer:
      trigger: {operations: [delete, update]}
      high: require_review
      approval_rule: payments-approvers
      review_mode: return   # hold (the default) or return
```

```
ERROR:  statement held for human approval: waiting for approval; resend the identical statement once it is approved (review 9f97…)
```

If the config has an `mcp:` block, the denial leads with the review id and
names the MCP tool that waits:

```
ERROR:  review 9f97…: waiting for approval; call the MCP tool review_wait with the review id, then resend the identical statement once it is approved (statement held for human approval)
```

The operator message goes last because the mysql client keeps only the
first 512 bytes of an error.

`review_mode` is valid only where a risk level asks for `require_review`;
elsewhere startup refuses it. A control plane serves `return` only to a
sidecar at 1.196.0 or later, and refuses the config for an older one.

**`return` applies to every client on the listener**, humans included. A
developer in psql gets the denial too, and has to run the statement again
after approval. Pick one:

- Give agents their own listener with `review_mode: return`.
- Keep `hold` on a shared listener and let each agent opt in.

**A client can pick its own mode.** It asks for `hold` or `return`, and that
overrides the listener for its statements:

| Protocol | How the client asks | Scope |
|---|---|---|
| http | header `x-hoop-review-mode: return` | one request |
| grpc, spanner | metadata `x-hoop-review-mode: return` | one call |
| postgres | `application_name` ends in `hoop-review=return` | the connection |
| mysql | connection attribute `hoop_review_mode=return` | the connection |

```bash
PGAPPNAME='billing-agent hoop-review=return' psql -h relay -p 15432 appdb
```

```
user:pass@tcp(relay:13306)/appdb?connectionAttributes=hoop_review_mode:return
```

The postgres token is the whole `application_name`, or follows a space or a
`;`, so the client keeps its own name in front. A value other than `hold` or
`return` is ignored and the listener mode applies: a typo must not turn an
agent's call into one that waits for a human.

Leaving the choice to the client is safe: both modes need the approval, only
the wait moves. The audit record carries `review_mode` and
`review_mode_source` (`listener` or `client`). Every http lane captures the
header, even with no `http:` block; a grpc or spanner lane that holds adds it
to the metadata allowlist. The header reaches policy and audit. It is kept
out of the analyzer prompt and the verdict cache key, so hold and return
share one classification.

**Find the review without parsing text.** On http and grpc a review
denial also carries the review in structured fields. http sends headers on
the 403; grpc sends the same keys, lowercase, as trailing metadata beside
`PERMISSION_DENIED`.

| Field | Value |
|---|---|
| `X-Hoop-Denied` | `review`; any other denial says `policy` |
| `X-Hoop-Review-Id` | the review id |
| `X-Hoop-Review-Status` | the plane's status, such as `PENDING` or `REJECTED`; absent when the sidecar could not read it |
| `Retry-After` | `5`, in return mode while the review is pending only |

The body stays the text message, so a client that knows nothing of reviews
still reads it. kubectl prints it as `Error from server (Forbidden): ...`.

**Read the status on the lane.** An http lane answers
`GET /.well-known/hoop/reviews/<id>` itself for a review filed on that lane,
with the `review_status` fields and `next` (see
[Agents over MCP](#agents-over-mcp)), never the statement. Another lane's
review reads as not found:

```bash
curl -s http://relay:18080/.well-known/hoop/reviews/9f97…
```

The whole `/.well-known/hoop/` prefix belongs to the sidecar on every http
lane and never reaches the upstream, so a route there cannot shadow one the
upstream serves. GET and HEAD only. It is answered only as the first request
on a connection: behind another one, the lane closes the connection
unanswered, because HTTP/1.1 pairs responses by order. curl and Go clients
resend on a fresh connection. A sidecar with no control plane answers
503. Like the MCP endpoint, it needs no credential: it answers by review id
only, and never with the statement. Other protocols read the status over
MCP.

**The retry contract.** An agent in return mode follows four rules:

1. **Resend the identical bytes.** The plane matches an approval on the exact
   statement. A reformatted statement (other whitespace, other quoting, a new
   comment, a new trace id) files a new review and pages the approvers again.
2. **Resend once, after approval.** Wait with the MCP tool `review_wait`, or
   poll `review_status` (see [Agents over MCP](#agents-over-mcp)). An
   approval releases one run.
3. **Do not resend a rejected or revoked review.** The decision is final for
   that review. A resend files a new review and pages the approvers again, so
   only a person asks again.
4. **Run a holdable statement in autocommit.** On postgres, mysql, mssql and
   mongodb a denial closes the connection, and the database rolls back any
   open transaction with it. The earlier statements of that transaction are
   lost, and the retry runs on a new connection. This holds for every denial:
   return mode, a hold that times out, a rejection.

Matching is on the exact bytes, so the retry must be the same statement, not
an equivalent one. Two consequences worth knowing: a client using prepared
statements sends the query with its parameters unbound, so an approval
releases that query shape rather than one set of values, and a statement
larger than 100 KB is refused by the plane rather than reviewed.

A statement that is not printable text, such as the protobuf body kubectl
sends for create, auth can-i and auth whoami, reaches the reviewer as a notice
line with its byte count, then the bytes: each byte that is not printable shows
as `\xNN` and a backslash as `\\`. The match stays on the raw bytes.

On an http lane the relay files the method, the target and the body, as
`POST /transfers?dry_run=false`, a blank line, then the body. The reviewer
reads that. The body is filed with `http.capture_body` off too: a lane where a
level asks for `require_review` captures request bodies by itself, and they
reach the analyzer, policy and the audit trail as `capture_body` would send
them. Response bodies still need `capture_body`. A statement that is not
printable text, such as a kubectl protobuf body, reaches the reviewer as a
notice line, then `\xNN` for each byte that is not printable; the match
stays on the raw bytes. Five consequences:

- A body larger than `http.max_body_bytes` (64 KiB unless set) is truncated
  by the codec, so the hold denies it without filing: the approval would
  bind to bytes nobody read.
- A query value the codec redacts (a token, a password) is filed redacted, so
  requests differing only in that value match one approval (EVL-310).
- A request carrying a trace id, a nonce or a timestamp never matches twice.
  Every attempt files its own review and pages the approvers again.
- A client that retries on its own timeout leaves two attempts in flight, and
  the approval releases whichever claims it first.
- A request that arrives in more than one read reaches the upstream in part
  before the gate decides: the request line, the headers and the start of the
  body. A denial does not recall them, so a route that acts on headers alone
  runs whatever the reviewer decides (EVL-309).

On a grpc lane the relay files the method path, a newline, then the message
as protojson. A spanner lane files the SQL alone when it reads SQL from the
message, and the grpc form otherwise. The reviewer reads that SQL, not its
parameters or the database it runs against, so an approval releases the
query on any bound values, as with prepared statements on postgres.
A hold needs `grpc.capture_payload`:
without it no message reaches the analyzer. The http consequences apply:

- A message larger than `grpc.max_payload_bytes` denies without filing.
- A message carrying a request id, a nonce or a transaction id never matches
  twice. Metadata is not filed, so a trace header does not break a match.
- The request headers reach the upstream before the gate decides; the message
  does not. The caller's deadline ends the wait.

On an ssh lane only `exec` holds: the analyzer classifies `exec_line` alone,
so env and sftp never reach it. A shell sends no statements, so nothing typed
in one is held. A holding ssh lane must drop `shell` from
`ssh.capabilities_allowed`.

**The approval is exact; the classification is not.** The verdict cache keys
on the statement SHAPE with literals stripped, so two statements differing
only in a literal share one classification. On a holding lane that cuts both
ways: a shape the model rated high holds every statement of that shape, each
filing its own review, while a shape it rated low is forwarded without a hold
even when a later literal makes it the dangerous one. `WHERE tenant = 'test'`
and `WHERE tenant = 'prod'` are one shape. The cache is off unless the config
turns it on; set `cache: {size: 0}` on a lane where every statement has to be
judged on its own, and pay one model call per statement for it.

Four things have to be true, and each missing one is refused at startup rather
than at the first held statement:

| | |
|---|---|
| a level asks for `require_review` | otherwise `approval_rule` names reviewers nobody consults |
| `approval_rule` is set, and not blank | spaces match no rule in the control plane |
| an ssh lane does not admit `shell` | a shell sends no statements, so what is typed in it walks around the hold |
| the sidecar has a control plane | there is nowhere else to file a review |

Everything else fails CLOSED, `fail_open` included: it answers for a model
vendor's outage, not for a human gate. A control plane that times out, refuses
or answers something unreadable denies, and so does a statement that could not
be classified at all, whether the provider failed, `max_calls` ran out or
`rate_limit` throttled it. On a lane that only warns or blocks, a spent budget
or an empty bucket still allows.

`mode: observe` is the one exception, and it files NOTHING. A dry run that
paged approvers about statements it then forwarded would be a dry run with
consequences; the lane records the hold as `guardrails.would_deny` and the
startup report says so.

It is per lane on purpose: the people who may release a statement against the
payments database are not the people who may release one against a reporting
replica, and a process-wide default would make the looser of the two the
accident. Editing it is a hot reload, not a restart: the block swaps with the
lane's rules, so a corrected reviewer group reaches the lane on the next
heartbeat.

The deprecated `type: ai_analysis` rule cannot hold. It carries no
`approval_rule`, so a review filed from one would name nobody who could
release it; a rule naming `require_review` is refused with a message pointing
at the listener block.

**Writing your own prompt.** Risk depends on what you are protecting, so the
risk guidance is replaceable at two levels.

`analyzer.prompt` at the top level is **process-wide**: it reaches every lane,
database and HTTP alike. Put deployment-wide facts there and nothing
protocol-specific, because the same words reach the model judging a SQL
statement and the model judging a JSON body. The listener block's own
`prompt:` is where protocol- and lane-specific wording belongs, and it wins:

```yaml
analyzer:
  prompt: |
    You are classifying traffic to a regulated production environment
    holding customer financial records. Anything that reads or modifies
    customer or payment data is at least medium risk.

listeners:
  - name: appdb
    # ...
    analyzer:
      trigger: {operations: [update]}
      high: block
      prompt: |
        You are classifying SQL against the customer ledger. An UPDATE
        with no WHERE clause is always high risk, and so is any schema
        change.

  - name: api
    # ...
    analyzer:
      trigger: {resources: ["/orders/**"]}
      high: block
      prompt: |
        You are classifying HTTP request bodies to the orders API. A
        payload that cancels or refunds in bulk, or that edits another
        tenant's records, is high risk.
```

Setting only the top-level `analyzer.prompt` is fine: the built-in guidance it
replaces covers SQL, HTTP and gRPC payloads, with separate high-risk examples
for each. Overriding it with database-only wording is the easy mistake: an
HTTP or gRPC lane then classifies JSON bodies against advice about `DROP` and
`TRUNCATE`.

A prompt replaces the **guidance** only. Two instructions are appended after
whatever you write and cannot be removed:

- Report the verdict by calling exactly one of the three risk tools. The tool
  choice makes the risk level an enum rather than a parsing problem: the level
  is which tool the model chose, so there is no free text to misread.
- Never quote a literal value from the statement in the title or explanation.
  That is a security property: the verdict reaches an audit record, and a
  title repeating the identifier it objected to has published that
  identifier.

Changing a prompt invalidates cached verdicts for the lanes it applies to, so
a reworded prompt takes effect on the next statement rather than after the
cache TTL. `/config` reports `custom_prompt: true` and never the text, for the
same reason it reports rule names and not their `pattern_regex`.

**Costs.** An ORM issues one statement shape thousands of times per session.
SQL cache keys strip literals; HTTP resources are normalized; HTTP and gRPC
payload bodies remain part of the key because stripping arbitrary JSON values
would merge requests whose risk can differ. Watch the hit rate at `/stats`
before enabling a blocking action:

```bash
curl -s localhost:19000/config | python3 -m json.tool   # what each lane sends
```

Verdicts land in the audit trail as `metadata.risk_level`, which rolls up to a
session's highest risk in `GET /api/sessions` and `GET /api/stats`. Beside it,
`metadata.ai_status` records what the analyzer did: `ok`, `cached`, `skipped`,
`budget_exhausted`, `rate_limited`, `refused` or `error`. It merges most-degraded-wins across
evaluators, so a second analyzer that succeeded cannot hide the first one's
outage. `metadata.ai_rule` names what produced the level — the LISTENER name
for an analyzer block, the rule name for the deprecated rule form — and
merges as a unit with `risk_level`, so the name always belongs to the level
shown. All three field names are unchanged from the rule-form era, so
dashboards and policies keyed on them keep working.

`ai_status` and `ai_rule` are the analyzer's OWN audit vocabulary, not the
finding it publishes to policy. The trail keeps the specific word, because an
operator tuning `max_calls`, one tuning `rate_limit` and one tuning
`send: refuse` are chasing different things. The finding maps all three onto
the generic `unavailable` and
puts the word in `reason`, so a Rego author never has to learn this package's
statuses to write a policy.

**Credentials never appear in the config.** `credentials_file` is a path; the
file must not be readable by group or other; the material is held in a type
that refuses to print through `%v`, `%#v`, JSON or `slog`; and `/config`
reports the endpoint host only. An endpoint URL carrying userinfo or a query
string is refused at startup, because that view sits beside a read interface
to the audit trail.

**Providers.** Four names, and the credential decides which one you want.

| `provider` | Model | Credential | Extra keys |
|---|---|---|---|
| `anthropic` | Claude | Anthropic API key | |
| `openai` | any Chat Completions endpoint | API key | |
| `gemini` | Gemini | Google API key | `api: developer` (default) or `api: vertex` (express mode, global) |
| `vertex` | Claude, Gemini, or a Model Garden open model | GCP identity | `project`, `region`, `publisher: anthropic` (default), `publisher: google` or `publisher: openapi` |

**Gemini** with an API key goes through `provider: gemini`. `api: developer`
is the Gemini Developer API on `generativelanguage.googleapis.com`, billed to
a Google account. `api: vertex` is Vertex AI in express mode on
`aiplatform.googleapis.com` with a Google Cloud API key, billed to the key's
project. Express mode is global: the URL names no location and Google routes
the call, so it offers no region selection and no VPC Service Controls. A
workload that must stay in one region uses `provider: vertex` with
`publisher: google` below, where `region` is part of the URL. Google
documents API keys for testing and ADC for production; a static key has no
identity to audit and no rotation of its own. The key travels in the
`x-goog-api-key` header, never in the URL.

**Vertex** authenticates with a GCP OAuth2 bearer minted from a service
account and refreshed automatically, so `-validate` mints one token to prove
the credential, the `roles/aiplatform.user` binding and the host clock before
anything serves traffic. Prefer Workload Identity and omit `credentials_file`.
The model calls and the token mint both verify against the process trust
pool, so behind an egress proxy that re-signs TLS, add its CA with
[`trust.ca_file`](#adding-a-ca-to-the-trust-store) rather than `SSL_CERT_FILE`.
`publisher: google` serves Gemini through the same bearer; the default,
`publisher: anthropic`, serves Claude. A GKE pod under Workload Identity, a
GCE or Cloud Run instance with an attached service account, and a pod outside
GCP holding a service-account key or a Workload Identity Federation
`external_account` file all reach Gemini this way, with no API key anywhere:

```yaml
analyzer:
  provider: vertex
  model: gemini-2.5-flash
  extra: {project: my-gcp-project, region: global, publisher: google}
```

`publisher: openapi` sends Model Garden open models, such as Llama, DeepSeek,
Qwen and gpt-oss, to Vertex's OpenAI-compatible Chat Completions endpoint
under the same bearer. Enable the model on its Model Garden card first.
Vertex routes that shared endpoint by the model's publisher prefix, so the
relay refuses a bare name at load. The analyzer forces a tool call
(`tool_choice: required`), so pick a model with function calling; a model
without it fails each statement with "called no risk tool".

```yaml
analyzer:
  provider: vertex
  model: meta/llama-4-maverick-17b-128e-instruct-maas
  extra: {project: my-gcp-project, region: us-east5, publisher: openapi}
```

#### Migrating from `type: ai_analysis` rules

The analyzer used to be spelled as a guardrail rule. That form is DEPRECATED
and still works: it loads, builds the same evaluator through the same code
path, and prints a deprecation naming its replacement (`-strict` turns the
warning into a non-zero exit). Both spellings can serve one lane while you
migrate, each as its own evaluator.

`hoop-inspect -migrate -config old.yaml -migrate-out new.yaml` does the move
below mechanically, refuses to guess where it would be lossy, and reports
what is left; the rest of this section is what it does and why. Move the
rule's fields onto the listener's `analyzer` block; they keep their names:

```yaml
# before
listeners:
  - name: appdb
    guardrails:
      rules:
        - name: risky-writes
          type: ai_analysis
          trigger: {operations: [delete]}
          high: block
          medium: warn
          message: refused by risk analysis

# after
listeners:
  - name: appdb
    analyzer:
      trigger: {operations: [delete]}
      high: block
      medium: warn
      message: refused by risk analysis
```

Three things change with the move, all visible rather than behavioral traps:

- `metadata.ai_rule` and the finding's `rule` carry the LISTENER name instead
  of the rule name, because a component has no rule name. The field names and
  everything else in findings, audit metadata and `/stats` stay the same.
- The call budget keys on the listener name. Two lanes that shared one rule
  name used to pay from one purse; two analyzer blocks never do.
- A rule in the TOP-LEVEL guardrails block used to reach every lane; the
  analyzer block is per listener, so write one on each lane that wants it.
  What was genuinely process-wide about the analyzer already lives in the
  top-level `analyzer` section.

A lane with several `ai_analysis` rules (say, different triggers with
different prompts) keeps them until it can express itself as one block;
`-validate` counts what is left to migrate on each lane:

```
appdb            postgres  enforcing 2 rule(s) + ai analyzer (and 1 deprecated ai rule(s))
```

### Agents over MCP

An agent that drew a `return` denial must learn when a person decides. The
`mcp:` block starts an MCP server with three read-only tools for that
(ADR-0021).

```yaml
mcp:
  listen: 127.0.0.1:8765    # the endpoint is http://<host>:8765/mcp
```

- `listen` is required. Remove the block to turn the server off.
- It needs a control plane, because review status comes from there. A config
  with the block and no plane is refused at startup, and so is a build that
  does not link the server. `hoop-inspect` and `hoop start sidecar` link it;
  a relay you embed through `daemon.Run` imports `sidecar/mcp` itself.
- Put the block next to the listeners: in the document the plane holds for
  this sidecar. The local file carries it only when it seeds the plane or
  the plane answers `load_from_disk`.
- A reload does not start, stop or move the server. Restart the process
  after you add, remove or change the block.
- A bind failure stops the process, as a listener's does.

**The endpoint has no authentication**, the same as the listener ports. A
caller reads every review of this sidecar: its id, status, listener name and
approval rule, never the statement. Bind it where only the agent reaches it: loopback when
the agent runs on the same host, a ClusterIP Service on Kubernetes, never a
public load balancer. The server refuses cross-origin browser requests, so a
web page cannot drive it from a victim's browser.

**Three tools.**

| Tool | Input | Does |
|---|---|---|
| `review_list` | `status`, `limit` (default 20, max 200) | lists this sidecar's reviews, newest first, across every listener |
| `review_status` | `id` | reads the review once |
| `review_wait` | `id`, `timeout_seconds` (default 60, max 300) | reads every 2 seconds until a person decides or the timeout ends |

`review_status` and `review_wait` return one review and what to do next.
`review_list` returns `{"reviews": [...]}` of the same shape:

```json
{
  "id": "9f97…",
  "status": "APPROVED",
  "listener_name": "payments",
  "approval_rule": "payments-approvers",
  "created_at": "2026-09-29T14:02:11Z",
  "decided_at": "2026-09-29T14:03:40Z",
  "next": "resend_identical_statement",
  "instruction": "Approved. Resend the identical statement, byte for byte, to listener payments. It runs once.",
  "timed_out": false,
  "waited_seconds": 42
}
```

| `status` | `next` |
|---|---|
| `PENDING` | `wait`: call `review_wait` again, do not resend |
| `APPROVED` | `resend_identical_statement` |
| `REJECTED`, `REVOKED`, `EXECUTED`, any other | `stop` |

`rejection_reason` is set on a rejection that gave one. `timed_out` and
`waited_seconds` come from `review_wait` only, and `timed_out: true` is not
an error: call again. Keep the 60 second default, because some clients drop
a tool call that blocks past 60 to 120 seconds. `review_wait` sends a
progress notification every 2 seconds to a client that asks for them.

Two answers are errors. "Not found on this sidecar" means stop: the plane
scopes the read to this sidecar's token, so another sidecar's review reads
the same as a wrong id. "The control plane is older than this sidecar" means
a person checks the review in the control plane.

The server never approves or claims a review. The resend runs
through the lane like any statement, so the analyzer, audit and masking
apply, and the resend spends the approval.

**Add it to the agent next to the DSN.** One entry per sidecar the agent
uses. One entry covers every listener and replica of that sidecar: the
server keeps no session, so it can sit behind a load balancer.

| The agent connects to | The agent's MCP entry |
|---|---|
| `postgres://agent@payments-sidecar:15432/payments?application_name=claude%20hoop-review%3Dreturn` | `hoop-reviews` at `http://payments-sidecar:8765/mcp` |

Claude Code, for one developer:

```bash
claude mcp add --transport http hoop-reviews http://payments-sidecar:8765/mcp
```

For a team, commit `.mcp.json` at the repository root
(`claude mcp add --scope project` writes it):

```json
{
  "mcpServers": {
    "hoop-reviews": {
      "type": "http",
      "url": "http://payments-sidecar:8765/mcp"
    }
  }
}
```

Cursor reads the same shape without `type`, from `.cursor/mcp.json` in the
project or `~/.cursor/mcp.json`. Any other client that speaks the Streamable
HTTP transport takes the same URL.

An agent that uses two sidecars needs two entries, for example
`hoop-reviews-payments` and `hoop-reviews-ledger`. The denial does not name
the sidecar, so name each entry after the DSN it pairs with, and tell the
agent to call the entry of the sidecar that denied.

**On Kubernetes**, set `listen: 0.0.0.0:8765` in the plane's document, so
the Service reaches the pod, and publish the port on the sidecar's Service.
With the chart, that is one `laneServices` entry:

```yaml
laneServices:
  mcp:
    enabled: true           # ClusterIP by default; keep it
    ports:
      - {name: mcp, port: 8765}
```

The agent's URL is then `http://<fullname>-mcp.<namespace>.svc:8765/mcp`.
`<fullname>` is `<release>-hoopsidecar`, or the release name alone when it
already contains `hoopsidecar`; `kubectl get svc` shows it.
Every pod in the cluster can reach a ClusterIP Service; a NetworkPolicy
narrows that to the agent. See the chart's
[README](../deploy/helm-chart/chart/sidecar/README.md#ports).

## Overlap with Envoy

Envoy already parses some of this.

`envoy.filters.network.postgres_proxy` parses SQL from Postgres `Query` and
`Parse` messages, emits `statements_select/insert/update/delete` counters, and
produces dynamic metadata as `table.db → [operations]` that the RBAC network
filter can act on. Within Postgres, at table-and-verb granularity, that is a
real capability.

The boundary:

| | Envoy | sidecar |
|---|---|---|
| Postgres SQL parse | `postgres_proxy`, best effort | full statement text |
| Postgres granularity | `table.db` + operation verb | statement, every effect, and relations split read/write |
| Postgres **response** | ✗ | result columns, row count, and masking by re-framing |
| HTTP request | `ext_authz`: method, path, headers, bounded body | same, plus normalized resource |
| HTTP **response** | ✗, ext_authz decides before the upstream is called | status, headers, body |
| Deny UX | RBAC/ext_authz drops or returns a bare 403 | operator-authored message |
| gRPC request/response messages | method and metadata matchers; proto scrubber removes configured fields on supported Envoy builds | descriptor-backed content policy in both directions and response value masking, standalone or behind Envoy |

On HTTP, Envoy's `ext_authz` covers request-side authorization well, and the
gaps named above are the narrow ones; Envoy is not blind here.

## Protocols

| Protocol | Request messages | Response messages | Stateful |
|---|---|---|---|
| `postgres` | `Query` ('Q'), `Parse` ('P'); handshake skipped | `RowDescription` ('T'), `DataRow` ('D'), and the three terminators that end a result set | yes |
| `mysql` | `COM_QUERY` (0x03), `COM_STMT_PREPARE` (0x16) and the prepared-statement commands (0x17, 0x19, 0x1a, 0x1c); handshake read for the negotiated capabilities, not skipped | column definitions and both row encodings, text and binary, for masking; the terminator that ends a result set | yes |
| `mongodb` | `OP_MSG` commands and the legacy `OP_QUERY` hello; document-sequence sections are reassembled into the command | cursor `firstBatch`/`nextBatch`, `findAndModify.value`, distinct values and inline map-reduce results for masking | yes |
| `mssql` | `SQLBatch` (0x01) and `RPCRequest` (0x03), reassembled across packets; login forwarded untouched | `COLMETADATA` (0x81), `ROW` (0xD1), `NBCROW` (0xD2) for masking; login replies scanned for a routing redirect | yes |
| `oracle` | Plaintext TCP Oracle Net (TNS/TTC) SQL calls from thin and OCI (thick) clients; negotiated TTC request layouts and cursor re-execution | Verified result-set columns and rows for character-value masking | yes |
| `http` | HTTP/1.x requests; HTTP/2 streams, each bridged into one HTTP/1.1 request | HTTP/1.x responses | no |
| `grpc` | request headers; decoded messages when capture is on | response trailers; decoded messages when capture or masking is on | yes, per HTTP/2 stream |
| `spanner` | like `grpc`, plus one SQL statement per query or DDL string extracted from known Cloud Spanner methods | as `grpc` | yes, per HTTP/2 stream |
| `ssh` | the command of an `exec`, the name of an `env`, the path of each file operation | none — the lane rewrites output in flight and records none of it | yes, per connection |

An `oracle` lane applies Oracle SQL classification to each decoded statement for
policy decisions. It masks supported character results (VARCHAR, CHAR and
national character data) only after validating the result layout; a policy
denial sends a native Oracle error for supported TTC layouts before closing
the session; an unsupported layout closes without an error frame. Unsupported
client TTC calls fail closed, and with masking configured an unrecognized
result layout is refused rather than forwarded unmasked. This is plaintext TCP
TNS/TTC inspection, **not** TCPS or Oracle native encryption/integrity (ANO);
neither encrypted path is inspected.

Oracle tracks at most 1,024 open cursors, 16 MiB of retained SQL and column
metadata, and 65,536 character cells per result; crossing a limit refuses the
stream rather than forwarding a result without inspection. With masking on, an
OCI row sent as a table row image that carries a non-NULL unselected column is
refused too: that value has no name or type a mask rule could match.

Supported servers are Oracle Database 23ai (Free) and 21c (XE), with
go-ora and python-oracledb thin clients and SQL*Plus 21 and 23. The codec
reads the integer encodings each session settles in data-type negotiation,
not the TTC version alone, so the same lane serves thin and OCI clients. An
OCI session below TTC field version 27 (23ai) other than 16 (21c) is
refused; no public image exists to capture 19c. OCI clients may run queries,
DML, MERGE, DDL, PL/SQL blocks and CALL. OCI array DML (more than one
iteration per call), PL/SQL with more than one bind, and DDL with binds have
no captured layout and are refused; with masking on, so are PL/SQL output
binds (`EXEC :v := ...`), whose values no mask rule can name. Each
negotiation message must end its packet, so no call can ride behind one. The
SQL a client asks the server to run at logon (`AUTH_ALTER_SESSION`, e.g.
`ALTER SESSION SET NLS_LANGUAGE=...`) reaches policy as a statement like any
other, so a rule that denies `ALTER SESSION` refuses those logins.

Oracle runs one statement per call: a call with two statements fails with
ORA-00933 and runs neither. The lane therefore never splits a call; its only
inner semicolons belong to PL/SQL, and the whole text is analyzed together.
A PL/SQL block (`BEGIN`, `DECLARE`, with or without a `<<label>>`) or a query
with `WITH FUNCTION`/`WITH PROCEDURE` is `unknown`, with the DML the scanner
sees as its effects, so a rule naming `delete` denies
`BEGIN DELETE FROM t; END;`. The `CREATE` of a stored unit is a `create`:
its body is compiled, not run. SQL*Plus 21 sends
`BEGIN DBMS_APPLICATION_INFO.SET_MODULE(:1,NULL); END;` at logon, so a rule
naming `unknown` refuses SQL*Plus 21 sessions.

`grpc` is the exception to this table's codec model. It has a canonical
`libhoop/v2/codec/types.GRPC` protocol value and its HTTP/2 endpoint and
reusable protocol mechanics live in `libhoop/v2/codec/grpc`, but it has no
`inspect.Codec`. The sidecar daemon injects statement, Gate, policy, audit,
and masking callbacks, so one denied RPC does not close sibling streams and
masked protobuf messages are re-encoded with correct lengths. A
descriptor set is mandatory for capture or masking; method-only policy works
without one. See [ADR-0013](https://github.com/hoophq/adr/blob/main/0013-grpc-terminates-http2-in-process.md).

```yaml
listeners:
  - name: billing
    protocol: grpc
    listen: 127.0.0.1:18443
    upstream: billing:50051
    downstream_tls:             # omit for h2c on loopback or a unix socket
      cert_file: /etc/hoop/billing.crt
      key_file: /etc/hoop/billing.key
    upstream_tls: {}            # TLS + ALPN h2 to the backend
    grpc:
      descriptors: /etc/hoop/billing.pb
      capture_payload: true
      max_payload_bytes: 65536
      metadata: [x-tenant-id]
```

`-validate` loads the descriptor set and prints each method, its message types
and maskable response paths. Unreadable, malformed, or import-incomplete sets
fail before the listener binds. Compare the printed method list with the
deployed API to catch a valid but stale set that omits newer RPCs.

`grpc.descriptors` takes one entry or a list; sets merge, shared imports
dedupe, and two copies of one file that differ refuse to load. An entry is
a file path or a URL a linked fetcher resolves at startup. `hoop-inspect`
and `hoop start sidecar` link `gs://`:

```yaml
    grpc:
      descriptors:
        - gs://acme-schemas/billing/v42.pb
        - gs://acme-schemas/ledger.pb?generation=1726480000123456   # pinned
        - /etc/hoop/local-override.pb
```

A bucket carries an artifact a ConfigMap cannot (1 MiB cap), and lets each
team's CI publish its own set without a redeploy of the sidecar's volume.
The read is one GET on the JSON API as a GCP identity: a service account
key inline in `GOOGLE_APPLICATION_CREDENTIALS_JSON` (the gateway's own
variable, so one Secret serves both processes; set but malformed is an
error, never a fallthrough), else Application Default Credentials —
Workload Identity, an attached service account, or
`GOOGLE_APPLICATION_CREDENTIALS`. The identity needs
`storage.objects.get` on the object (`roles/storage.objectViewer`). There
is no anonymous read. `?generation=N` pins one version; any other query
parameter, or one the URL parser cannot decode, is refused, so a typo
cannot read the current version while the config appears pinned. The
fetch happens once, when the lane's endpoint is built, under the same
two-minute budget for credential discovery, the token exchange and the
read: the schema is bound into the server when it is built, so a
changed `descriptors` list is restart-bound drift on the heartbeat, and a
new version published behind an unpinned URL is applied by a restart, the
way a replaced file is. `-validate` performs the fetch, so a wrong object
name or a missing IAM binding fails there with the URL in the message. A
scheme this binary does not link (`s3://`) is refused at config validation
naming what is linked; the fetcher lives in the nested module
`descriptors/gcs`, and a binary that does not import it resolves no URL.

`-grpc-discover <listener>` bootstraps that set when the upstream exposes
gRPC server reflection: it dials the named lane's upstream with the lane's
own `upstream_tls` facts, prints every method with its maskable field
paths, and with `-grpc-discover-out billing.pb` writes the descriptor set
for the operator to review and pin as `grpc.descriptors`. Discovery is an
offline tool, not a lane capability — ADR-0013 keeps runtime lanes on
pinned files so the policed service cannot rename fields out from under
the mask rules.

The Postgres codec is stateful because one `RowDescription` describes every
`DataRow` after it, and those land in different TCP reads. That is why the
registry hands out a factory rather than an instance: two connections sharing
one codec would corrupt each other's reassembly, and one tenant's SQL would
surface in another tenant's audit trail. Give every connection its own.

### spanner: GoogleSQL over the gRPC lane

`protocol: spanner` runs the same in-process HTTP/2 endpoint as `grpc` —
same `grpc:` block, same descriptor plumbing, same `-grpc-discover` — but
reads through the RPC to the GoogleSQL inside it. On
`google.spanner.v1.Spanner` (`ExecuteSql`, `ExecuteStreamingSql`,
`PartitionQuery`, `ExecuteBatchDml`) and
`google.spanner.admin.database.v1.DatabaseAdmin` (`UpdateDatabaseDdl`,
`CreateDatabase`) each SQL string in a captured request message becomes its
own statement: `Text` is the query, the GoogleSQL lexer supplies the
operation and relations, and `spanner.sql_index` numbers it within the
message. Operation and table rules therefore work on Spanner SQL — a rule
naming `delete` refuses `ExecuteSql` carrying a DELETE, and a batch is
refused whole when any member denies, because it commits whole upstream.

SQL extraction needs payload capture, which needs `grpc.descriptors`
(ADR-0013: schema-less protobuf walking is unsound); without capture the
lane still fences methods like a `grpc` lane. Methods carrying no SQL keep
the generic per-message statement. A statement the lexer cannot read is
`unknown` with the reason in `sql.incomplete` — fail-closed, so a rule
naming `unknown` refuses it.

A Spanner database is created as GoogleSQL or as the PostgreSQL interface,
and one instance holds both; the data plane never says which. Read
PostgreSQL with the GoogleSQL lexer and `"songs"` is a string literal, so a
table rule fencing songs never fires. The dialect is therefore
configuration, keyed on the database resource name every SQL-bearing
request carries (`session`, `database`), never inferred from the text a
client controls:

```yaml
  - name: spanner
    protocol: spanner
    spanner:
      dialect: googlesql                 # lane default; absent block = googlesql
      databases:
        projects/p/instances/i/databases/ledger-pg: postgresql
```

`dialect: per_database` is the fail-closed shape: SQL against a database
not listed is `unknown` with the reason in `sql.incomplete`. `CreateDatabase`
is the one request that declares its dialect (`database_dialect`), and the
lane believes it for that request's statements. Each extracted statement
records `spanner.dialect` and `spanner.database` in its metadata, so the
trail says which lexer read it.

### ssh: the lane that is one end of the connection

`protocol: ssh` is not a relay. An SSH connection is encrypted end to end, so
nothing in the middle can read a command — to see one at all the sidecar has
to BE one end of it. The lane terminates the handshake, verifies the client's
certificate against a CA it trusts, and runs each admitted capability itself.
libhoop owns the mechanics; this module owns every decision. See
[ADR-0015](https://github.com/hoophq/adr/blob/main/0015-ssh-terminates-at-the-sidecar.md).

```yaml
listeners:
  - name: prod-endpoint
    protocol: ssh
    listen: 0.0.0.0:2222
    ssh:
      host_key: /etc/hoop-inspect/keys/endpoint_host_key
      trusted_ca: /etc/hoop-inspect/keys/hoop_ca.pub
      capabilities_allowed: [shell, pty, exec, env, sftp]
      identity:
        subject: key_id
        groups: principals
    guardrails:
      rules:
        - name: no-preload-injection
          type: pattern_match
          pattern_regex: '^(LD_PRELOAD|LD_LIBRARY_PATH)$'
          operations: [env_set]
          message: this environment variable is not permitted
```

**Certificates only.** There is no password method and no
`authorized_keys` list. A connection presents an OpenSSH user certificate
signed by `trusted_ca` or it is refused at the handshake, and the login name
it asks for must appear in the certificate's principals.

**And the certificate has to name someone.** `identity.subject` picks the
field, `key_id` by default, and a certificate that leaves it empty — an
`ssh-keygen -I ''`, or an extension a CA has not rolled out — is REFUSED
rather than admitted as `anonymous`. An `email` mapping satisfies it too;
either one names a principal. The trail is half the reason: the other half is
that `PolicyContext` omits an empty `subject`, and a Rego rule reading an
absent key does not fire, so a deny keyed on the subject would pass a session
it was written to stop.

**`operations` is what scopes a rule here.** One SSH lane emits three kinds of
text — a command line for `exec_line`, a variable name for `env_set`, a path
for every `sftp_*` — so a pattern written for one of them has to say which.
Unscoped, it is evaluated against all three.

**Five rule types are refused at load**: `table`, `http_resource`,
`http_status`, `http_header` and `grpc_status`. SSH has no relations, no
request path, no header and no RPC, so each would load and never fire. An
sftp path is matched with `pattern_match`, not with `table`.

**`capabilities_allowed` is tri-state.** Omitted admits the five capabilities
this version delivers; `[]` admits none, which is how a jump host drops the
shell; a list admits those. Naming a capability this version does not deliver —
`x11`, `agent_forward`, `remote_forward`, `subsystem` — fails at load rather
than being admitted and then quietly not working.

**Forwarding is not a capability.** `destinations_allowed` decides where a
client-opened forward may be carried, and an absent or empty list denies every
one of them. Entries are `network[:port]` or the single word `any`; IPv6 needs
no brackets, because the port is read after the prefix length
(`2001:db8::/32:22`). The check is handed the address the endpoint WILL dial,
resolved once, and that same address is connected to — checking a name and
dialling it again would leave a window where the two disagree.

```yaml
  - name: prod-bastion
    protocol: ssh
    listen: 0.0.0.0:2222
    ssh:
      host_key: /etc/hoop-inspect/keys/bastion_host_key
      trusted_ca: /etc/hoop-inspect/keys/hoop_ca.pub
      capabilities_allowed: []          # optional: empty, not omitted, so no session here
      destinations_allowed:
        - 10.0.0.0/8:2222
```

**A session runs as the login name, as it does under `sshd`.** The name the
client asked for is looked up per connection, after the certificate's
principals have already vouched for it, and a login that is not an account on
this host is refused. There is no key for the account, no default and no
fallback to the sidecar's own user.

What bounds the set of accounts is the account this process runs as.
Unprivileged, it can serve only itself — the container's `USER` line is the
policy. As root it serves any account on the host, at the cost of running the
whole pre-authentication surface privileged.

The session also takes the account's own login shell, home directory and
environment. A shell that does not exist or is not executable refuses the
session, `/sbin/nologin` disables an account here exactly as it does
everywhere else on the host, and a home directory that is missing starts the
session in `/` rather than failing it.

File transfer is the one capability that runs in-process rather than in a
child, so it is served only for a login that resolves to the account this
process already IS; `-validate` reports that uid and gid whenever `sftp` is
admitted, and libhoop refuses every other login at the request.

**Masking is length-preserving or it is refused.** `strategy: mask` is the only
strategy an ssh lane can carry, and its `mask_char` must be a single byte: the
lane rewrites a byte stream in place, so a replacement of a different size
shifts everything after it and desynchronizes a terminal's escape sequences.
A rewrite that comes back a different length fails the stream closed rather
than forwarding a corrupted one. Terminal output and file downloads are masked
by the same rule set; client input is never rewritten, because changing what a
user typed is a denial wearing a redaction's clothes, and the guardrails
already refuse.

**`upstream`, `upstream_tls`, `downstream_tls` and `identity_header` are
refused** on an ssh lane. There is no fixed backend, SSH negotiates its own
transport, and the identity comes from the certificate this listener verified
itself.

#### What the trail holds, and what it does not

**v1 records no session content.** There is no recorder, no replay, and no
setting that would add one: that needs a store, a retention policy, a read
path and a decision about who may replay another person's terminal, and
landing the weakest version of each as a side effect of the first SSH lane is
what ADR-0015 refuses.

| Capability | What is recorded |
|---|---|
| `exec` | the command in full, as a statement with its verdict. Its output is masked in flight and not retained |
| `env` | the variable name as the statement, the value beside it. A sink with `RedactStatements` fingerprints that value, because it is statement content |
| `sftp` | one statement per operation per path — both ends of a rename — plus the operation, path, direction and byte count of each transfer. The file's bytes are never recorded |
| `shell`, `pty` | events only: the open, the terminal geometry, the duration and the byte counts. No keystrokes and no output, so there is nothing to replay |
| forwards, refused capabilities | thin metadata: a destination, a reason, a byte count. Never the relayed bytes |
| a refused connection | `session_start`, one `connection_refused` activity carrying the login asked for and the reason, then the close. libhoop closes a handler even when the connection is refused, so the session ends either way; without the middle record a turned-away login reads as a connection that did nothing |

Everything that is not a statement is written as `kind: activity`, with what
happened in `metadata.activity`.

### Postgres: trace ids from the startup packet

A postgres lane reads the client's StartupMessage before the gate sees a byte.
It records `user` as the principal, and **by default every setting the client
sends in `options`** onto the session's metadata, so every statement record,
the `session_end` record and OPA's `input.context` carry them. No config is
needed:

```bash
PGOPTIONS='-c claude.session.id=xyz1234678' psql -h 127.0.0.1 -p 15432 appdb
# DSN form: postgres://u@host:15432/appdb?options=-c%20claude.session.id%3Dxyz1234678
```

```json
{"kind":"statement","principal":"alice","statement":"SELECT 1",
 "metadata":{"postgres.option.claude.session.id":"xyz1234678"}}
```

Postgres accepts a dotted setting name as a custom setting, so the backend runs
with it too and `current_setting('claude.session.id', true)` reads it inside the
database.

The `postgres` block narrows or turns off the default:

```yaml
listeners:
  - name: appdb
    protocol: postgres
    listen: 127.0.0.1:15432
    upstream: appdb:5432
    postgres:
      startup_metadata:                 # a list records ONLY these
        - option: claude.session.id     # from options "-c claude.session.id=..."
          as: claude.session.id         # the metadata key; absent, postgres.option.<name>
        - parameter: application_name   # a plain startup parameter
```

| `startup_metadata` | Records |
|---|---|
| absent (or no `postgres` block) | every `options` setting, as `postgres.option.<name>` |
| a list | only those fields |
| `[]` | nothing |

`startup_metadata:` with no value is refused: it reads as absent, which is the
opposite of what it looks like.

- **Options are read the way the backend reads them**: words split on
  whitespace with `\` escaping, then `-c name=value`, `-cname=value` or
  `--name=value`. Names are recorded lowercase with `-` read as `_`, and the
  last assignment wins. Reading stops where the backend would refuse the
  connection (a non-switch word, `--`, an unknown switch), because no statement
  can run under what follows. A setting the client also sends as a startup
  parameter of its own (`claude.session.id=...` beside `options`) records that
  parameter's value, because the backend applies it after `options`; of two
  such parameters, the later wins, names compared case-insensitively.
- **`option`** names one setting, matched the same way. **`parameter`** names a
  StartupMessage parameter, matched exactly; parameters are only recorded when
  a list names them.
- **`as`** is the metadata key. Absent, it is `postgres.option.<name>` or
  `postgres.parameter.<parameter>`, the key the default writes, so narrowing a
  lane keeps the keys a dashboard queries. A key the relay writes into
  `input.context` itself (`principal`, `subject`, `session_id`, and the rest)
  is refused at load: the value is the client's, and that key would let the
  client name its own principal. The default's keys all sit under
  `postgres.option.`, so a client-chosen setting name cannot reach one.
- A source the client did not send records no key, not an empty value. A value
  longer than 256 bytes is cut at a character boundary. The default records as
  many settings as the client sends, bounded by the 10000-byte StartupMessage.

Where the values appear:

| Place | Carries them |
|---|---|
| each `statement` / `violation` event, and `session_end` | yes |
| `session_start` | no: it is written before the StartupMessage is read |
| the session row in `GET /api/sessions` and `/api/sessions/{id}` | once the session ends; the row takes `session_end`'s metadata, never a statement's |
| OPA `input.context` | yes, from the first statement |
| the `session opened` / `session closed` log lines | yes, under one `metadata` object |

```json
{"msg":"session opened","session":"de2f…","principal":"alice",
 "metadata":{"postgres.option.claude.agent.id":"agent-7","postgres.option.claude.session.id":"xyz1234678"}}
```

**These values are claims.** Postgres authenticates the user, not the options
beside it, so a lifted value is a label for tracing and never an identity. It
lands in `metadata`, not in `identity`. PgBouncer in front of
the backend refuses an unknown `options` parameter unless
`ignore_startup_parameters` lists it, and listing it drops the value before the
database sees it.

**The default records whatever the client puts in `options`**, `search_path` and
application settings included, in the audit trail, in what OPA receives and in
the process log, which your log pipeline ships with its own retention and
access rules. `audit.redact_statements` covers statement text, not metadata. A
lane whose clients may carry a secret in `options` should name the settings it
wants, or write `[]`.

### MySQL, and the three ways a session goes dark

MySQL is stateful for a harder reason than Postgres. A pgwire message is
self-describing on the first byte of the connection and on the last: the tag
says what it is, the length says where it ends. MySQL bytes mean different
things depending on what the handshake negotiated and on what the client last
asked for. `CLIENT_DEPRECATE_EOF` decides whether a result set ends with an
EOF packet (0xFE) or an OK packet beginning with the same 0xFE byte. A `0x00`
first byte opens an OK packet after a `COM_QUERY` and a column count after
nothing at all. So the codec reads the handshake rather than skipping it, and
tracks the command in flight — one codec per connection, from the factory the
registry hands out.

**A message is not a packet.** A payload of exactly 16 MiB − 1 means "there is
more", and the reader concatenates until a payload comes in short — which may
be an EMPTY packet when the message length is an exact multiple. A decoder
that treats every packet as a message reads the second half of a large
statement as a command byte followed by SQL, which is how a spliced
`DROP TABLE` gets past a classifier.

**One `COM_QUERY` can be several statements.** `CLIENT_MULTI_STATEMENTS` is
negotiated by Connector/J and most ORMs by default, so
`SELECT 1; DROP TABLE users` arrives as a single command. The seam injects
`lexer.Split` for exactly this: without it the codec classifies the payload by
its leading verb and the drop reaches the server having been evaluated as a
select. Splitting is the lexer's job rather than the codec's because getting
it right needs MySQL's quoting rules — a naive split on `;` cuts inside a
string literal.

**`COM_STMT_EXECUTE` does not carry its SQL.** It names a statement by the
numeric id the server assigned in reply to an earlier `COM_STMT_PREPARE`, so
the codec keeps the prepare's text against that id and attributes the execute
to it. A prepare the codec never saw — a connection adopted mid-flight — makes
the execute an `unknown` operation, which a rule naming `unknown` refuses.
That is the fail-closed direction.

**Three negotiated features are refused rather than forwarded**, each with
`ErrStreamUnsafe`, which the gate turns into a denial regardless of policy:

- **`CLIENT_COMPRESS`** replaces the packet framing itself from the first byte
  after the handshake response, so nothing after it is readable.
- **A client-initiated TLS upgrade.** The client sends the first 32 bytes of a
  handshake response and starts a TLS handshake on the same socket. This one
  is a deployment fault with a fix, and the message says so: terminate the TLS
  in front of the relay, and the codec is handed plaintext and never sees the
  flag.
- **`LOAD DATA LOCAL INFILE`.** The server answers with a filename and the
  client streams that file back as raw packets carrying no command byte. It is
  also the long-standing attack — a malicious server can send the request in
  reply to any query — so refusing it is a control and not only a parsing
  convenience. The statement that provoked it is already in the audit trail;
  the transfer never starts.

**MySQL masks its responses** by the same re-framing mechanism Postgres uses.
Every value in a text row is length-prefixed and so is the packet holding it,
so a changed row is rebuilt with both recomputed; patching in place leaves the
client reading a declared length that no longer matches and dropping the
connection. Rebuilt rows keep their original sequence ids, because MySQL
requires them consecutive within a command and renumbering one would mean
renumbering every packet after it.

Binary rows — what a prepared statement returns — mask only where the wire
encoding is already a length-encoded string. An `INT` column is four
little-endian bytes, and writing a redaction token there is not a long
integer, it is a client desynchronized for the rest of the connection. Every
other column is measured so the walk stays aligned and forwarded unchanged. A
number carrying a secret is a real gap, and the honest one: the alternative is
a protocol error the user reads as an outage.

**Commands are queued, not latched.** A client may send the next command
before the previous reply arrives — go-sql-driver closes a statement straight
behind its execute, Connector/J batches, and a cursor loop issues the next
fetch while the last batch is still streaming. The codec keeps the commands
awaiting a reply in order and pairs each reply with the oldest, advancing on
the packet that completes it (the final terminator, or an OK/EOF without
`SERVER_MORE_RESULTS_EXISTS`). Commands the server never answers — `COM_QUIT`,
`COM_STMT_CLOSE`, `COM_STMT_SEND_LONG_DATA` — are not queued. More than 1024
unanswered commands is refused as malformed.

**Server-side cursors are masked.** A `COM_STMT_EXECUTE` with a cursor flag
(Connector/J `useCursorFetch=true`) returns the column definitions and no rows;
the rows come back in `COM_STMT_FETCH` replies with no definitions of their
own. The codec retains the definitions while `SERVER_STATUS_CURSOR_EXISTS` is
set and releases them on `SERVER_STATUS_LAST_ROW_SENT`, so fetched rows are
masked by the same column names. A fetch on a cursor the relay did not see
open is refused: its rows would have no name to match.

### MongoDB, correlated commands and topology

MongoDB multiplexes requests on one socket. A response names the request it
answers through `responseTo`, so one codec instance sees both directions and
keeps the command against its request id. That state tells the response path
whether a BSON array is a cursor batch, a distinct result or ordinary command
metadata. A policy denial uses the same request id in a native OP_MSG command
error (`code: 13`, `Unauthorized`); without it the driver discards the reply as
unrelated and reports a lost connection instead of the operator's message.
Correlation is bounded to 1,024 outstanding requests and 32 MiB of retained
command metadata per connection. Exceeding either limit closes the session
fail-closed instead of turning pipelining into unbounded relay memory.

An exhaust stream chains, and the streaming `hello` every driver runs on its
monitoring socket is one: the client sets `exhaustAllowed`, and each reply
after the first answers the PREVIOUS REPLY's id rather than the client's
request. The codec follows that chain, so a stream costs one outstanding slot
however long it runs, and an idle connection is not torn down between
heartbeats. A request id that collides with a live chain is refused, because
`responseTo` carries nothing else to tell the two apart.

Modern drivers send commands as `OP_MSG`. Bulk inserts, updates and deletes
carry their documents in kind-1 document-sequence sections rather than the body
document, so the codec joins those sections before classification and audit.
`explain` is unwrapped to the command it can execute, and `applyOps` is expanded
into each embedded mutation so a delete or drop rule cannot be bypassed.
`OP_QUERY`/`OP_REPLY` hello remains supported because current servers still
permit that one removed opcode; every other legacy write opcode is refused.
SCRAM and heartbeat commands are parsed for framing and correlation but never
written into statement audit records.

MongoDB responses are BSON, so changing a string requires rebuilding the BSON
document and the enclosing 16-byte message header. If OP_MSG carries a CRC-32C
checksum, the codec recomputes it over the rebuilt message. Masking walks string
values in cursor `firstBatch` and `nextBatch`, `findAndModify.value`, distinct
values and inline map-reduce results. A column rule matches the BSON field name,
including nested fields. Nulls, numbers, ObjectIDs, dates, binary values and
every other typed BSON value remain unchanged; replacing one with a redaction
string would silently change the application's data model.

`OP_COMPRESSED`, unknown OP_MSG flags and removed write opcodes return
`ErrStreamUnsafe`. Forwarding any of them would run commands or return values
the relay cannot inspect. Disable MongoDB compressors in the client rather than
allowing the lane to go dark.

Clients must use `directConnection=true`, or a topology whose advertised member
addresses route back through the sidecar. A replica-set client is expected to
follow the `hosts` and `primary` addresses from the server's hello response;
when those addresses point at MongoDB directly, the next socket bypasses this
relay. The sidecar cannot infer or rewrite its externally reachable address, so
the connection option is part of this deployment contract.

### MSSQL, and the Kerberos login

TDS gives the SSPI exchange its own packet type, and that fact alone lets
integrated authentication cross the relay with **no Kerberos code in this
library**. LOGIN7 (`0x10`) and each SSPI continuation (`0x11`) carry no SQL, so
the relay forwards them verbatim. Inspection begins at the first SQLBatch
(`0x01`) or RPC (`0x03`). The protocol's own message typing draws that
boundary, so no heuristic guesses where the ciphertext stops.

A relay could do no more here. The SSPI blob is a service ticket bound to the
server's SPN: relayable, and beyond minting, reading or editing.
`mssql.DetectSSPI` reports that a login is integrated, which serves the audit
trail and a clear error message. It cannot report who: under integrated auth
the username field sits empty, because the name lives inside the ticket.

**The codec passes the encrypted login through and keeps reading.** PRELOGIN's
`ENCRYPT_OFF` says "encryption off" and means "encrypt the login only": MS-TDS
3.2.5.3 puts the first LOGIN7 packet inside TLS and leaves every other packet
in the clear. A SQL Server with TLS administratively disabled still negotiates
it, minting a self-signed certificate at startup for the credentials, and
go-mssqldb defaults to it, so every Go client meets this on an ordinary 7.x
lane.

The handshake rides inside `0x12` packets, which the codec already forwards.
The bytes after it break the parse: the client inverts the nesting and writes
TLS records to the socket raw, with no TDS header. A decoder that walks them
as packets loses its place, and the relay stops seeing the session while the
socket keeps carrying it: no statements, no policy, no masking, no audit
trail.

So the codec walks that region by TLS's own record framing, finishes on the
byte where plaintext resumes, and inspects everything after it. Statements
from such a session carry `mssql.login_encrypted`, so an operator reading the
trail sees the window nobody could observe. The pass-through opens once,
during the login, and stays bounded; the codec answers ciphertext before the
login, after it, or past the bound with `ErrStreamUnsafe`, because nobody can
inspect a session that never returns to plaintext.

**The codec refuses one thing outright.** A login response carrying a routing
ENVCHANGE tells the driver to reconnect elsewhere, and drivers obey without
telling the user. Forward it and the client lands on a socket the relay does
not hold, where the session continues with no policy, no masking and no audit,
leaving no trace that it stopped being watched. The codec returns
`ErrStreamUnsafe`, the gate turns that into a denial regardless of policy, and
the connection ends with a message naming the redirect target. No rule enables
this behaviour and none can switch it off.

**MSSQL masks its responses**, by the same re-framing mechanism Postgres uses
and against a harder framing. TDS nests two: an 8-byte packet header wrapping
a token stream, where one `ROW` spans packets and one packet holds several
tokens. Changing a value changes the token's length, which changes how the
tokens repack, so the codec strips the headers, rewrites the token stream, and
lays fresh packets over the result. Patching bytes in place cannot work,
because a longer value has nowhere to go.

A column type it cannot measure (SQL_VARIANT, XML, UDT) stops the rewriting
for that connection. Guessing a length would desynchronize the client, which
turns a privacy gap into a lost session. Statements and policy carry on; only
masking steps aside. Whatever was already rewritten is kept and emitted, so a
value masked earlier in a response stays masked when an unmeasurable token
turns up later in the same one.

A worked deployment, with Envoy terminating TDS 8.0, a Kerberos client and an
AD domain controller, lives in
[`deploy/docker-compose/envoy-stack/mssql`](../deploy/docker-compose/envoy-stack/mssql).

Adding another protocol is **not** a one-package change. The `Codec` interface
and policy vocabulary are protocol-agnostic, but everything a lane needs around
the decoder is keyed by protocol and each omitted piece fails differently:

| Add | Where | Symptom if you skip it |
|---|---|---|
| the decoder | `libhoop/v2/codec/<name>` | nothing to register |
| a `Protocol` constant | `libhoop/v2/codec/types`, aliased in `inspect/wiretypes.go` | callers spell the protocol as a string literal |
| the registration seam | `sidecar/codec/<name>`, and its import in `codec/all` | `inspect.New` refuses the protocol, so the lane will not start |
| classification | a SQL `lexer.Dialect` selected by `inspect.AnalyzeSQL`, or the wire codec's native command classifier | operations and relations stay `unknown` |
| a deny frame | `proxy/deny.go`; include request correlation when the protocol requires it | a denial closes the socket with no useful message |
| an analyzer content builder | `analyzer/content.go` | an analyzer on the lane classifies nothing; startup refuses the lane rather than let it run silent |

An ENDPOINT protocol — one that terminates in-process rather than relaying,
so `grpc`, `spanner` and `ssh` — skips the decoder, the registration seam and
the deny frame, and picks up four of its own. Every one of them fails quietly
when omitted, which is why they are listed rather than left to be noticed:

| Add | Where | Symptom if you skip it |
|---|---|---|
| a codec-registry carve-out | `daemon/config.go`, beside `isGRPCTransport` | the lane is refused at load as an unsupported protocol |
| the server build and its two call sites | `daemon/<name>.go`, registered in the `-validate` pass and the run loop in `daemon/daemon.go` | the config validates and the listener never binds |
| protocol-aware rule refusals | `policy/<name>.go` | a rule type the lane cannot read loads, evaluates and never fires |
| a `MaskSupported` answer | `gate/gate.go` | `mask.rules` on the lane is refused as unsupported, or accepted and never applied |

Masking needs no registration: the gate asks the codec for a `Reframer`, so a
decoder that can rebuild its rows masks, and one that cannot has its
`mask.rules` refused at startup.

The decoders ship in `github.com/hoophq/libhoop`, a separate private module
that imports nothing from here. The packages under `sidecar/codec/` are the
seam: they register each decoder and attach any classifier the codec cannot own.
Import those, not libhoop directly. Import only what you need: a listener that
speaks Postgres imports `codec/postgres` and never links the other protocols.

```go
import _ "github.com/hoophq/hoop/sidecar/codec/postgres" // postgres only
import _ "github.com/hoophq/hoop/sidecar/codec/all"      // every shipped protocol
```

## The Statement

```go
type Statement struct {
    Protocol  Protocol          // postgres | mysql | mongodb | mssql | http
    Direction Direction         // client | server
    Text      string            // SQL, MongoDB Extended JSON, or an HTTP request line
    Operation Operation         // the most consequential effect, not the leading verb
    Effects   []Operation       // every operation performed anywhere in the statement
    Relations []Relation        // {name, access}, write dominating read
    Tables    []string          // Relations flattened, or the HTTP resource
    Database  string            // when the protocol states it
    HTTP      *HTTPDetail       // http only; nil for the wire-database codecs
    Result    *ResultDetail     // response side: columns and row count, never values
    Metadata  map[string]string // protocol-specific, documented per codec
}
```

`Result` is what makes a response-side database rule possible: `SELECT *` or a
MongoDB `find` request does not name every field it returns, so "this result
contained a column named ssn" is a question no request-side rule can answer. It
carries field or column names and a row count, never the values.

MongoDB fills `Operation`, `Effects` and `Relations` directly from the command
document. `Text` is deterministic Extended JSON with every document-sequence
section restored, so a bulk write is audited and analyzed as the complete
command the server executes rather than only its small body section.

For SQL one scan of the statement text fills four fields, and they are not
four views of the same fact:

- **`Operation` is the most consequential EFFECT**, not the leading verb.
  `WITH x AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM x` is a
  `delete`, because a policy asking "may this run" is asking about the effect
  and not about the spelling.
- **`Effects` carries the full set**, every operation performed anywhere in
  the statement. Read it to tell a statement that only deletes from one that
  deletes and selects.
- **The vocabulary is closed**, and wider verbs fold into it. The scanner
  models `MERGE`, `COPY` and `EXPLAIN`; `Operation` and `Effects` never report
  them, because `MatchOperation` and the AI trigger compare for equality and a
  config naming `update` cannot name `merge`. A `MERGE` is an `update`, its
  `WHEN MATCHED THEN DELETE` branch still adds `delete`; `COPY ... FROM` is an
  `insert` and `COPY ... TO` a `select`; a plain `EXPLAIN` is a `select`,
  because it plans and does not execute. Nothing folds onto `other`, which
  ranks below every real verb and would hide a bulk load.
- **`Relations` carries `{name, access}`**, deduplicated and lowercased, with
  write dominating read for a relation that is both.
  `INSERT INTO staging SELECT * FROM customers` writes `staging` and reads
  `customers`.
- **`Tables` is `Relations` with the access dropped**, kept so rules written
  before the split keep matching. Empty means "could not tell", never
  "touches nothing".
- **`Operation == unknown` with `metadata["sql.incomplete"]` is the
  fail-closed signal.** The scanner met a statement whose effect is decided
  at runtime and says so instead of guessing; the metadata value is the
  reason. A rule naming `unknown` refuses those statements, and one that does
  not name it accepts that risk explicitly.

Comments and string literals are stripped before any of it, so
`SELECT 'DROP TABLE customers'` is a `select`. Prefer `MatchOperation` to
`MatchDenyWords` for verbs.

### Why this is not a parser

A SQL grammar is large, dialect-specific and a permanent maintenance burden.
The question a policy asks is much smaller: which relations does this
statement write, and which does it read. Verbs and the relation names beside
them are token-local, so the scanner answers that question without building
an AST.

It does need a stack. One integer of parenthesis depth says "somewhere inside
parentheses" but not "inside a CTE body", which is why a data-modifying CTE
used to read as a plain select. A stack of LABELLED regions costs one byte
per nesting level, and that one bit of context per level is what made
`WITH x AS (DELETE FROM t) SELECT` readable.

It models no expressions at all: no precedence table, no operator handling
past "these bytes end a token". Expression grammar is where most of a real
parser's cost lives, and none of it answers the question above.

`Dialect` exists because one byte means different things per engine. `[`
opens a quoted identifier in T-SQL and is an array subscript in PostgreSQL:
treating it as an identifier everywhere mangles `SELECT tags[1] FROM t`,
treating it as an operator everywhere loses `[dbo].[customers]`. Only the
lexical rules differ; the analysis after them is shared.

MySQL diverges in five places, and each one is a statement that would
otherwise execute unseen rather than a matter of taste:

| Rule | MySQL | Elsewhere | Cost of using the wrong one |
|---|---|---|---|
| `` `name` `` | quoted identifier | not a delimiter | ``DELETE FROM `select` `` loses its relation |
| `#` to end of line | comment | live operator (PostgreSQL spells XOR with it) | `SELECT 1 # DROP TABLE t` reports a drop nobody ran |
| `--` | needs whitespace after it | opens a comment either way | `SELECT 1--2; DELETE FROM t` hides the delete inside a comment |
| `\` in `'...'` | an escape, unless `NO_BACKSLASH_ESCAPES` | an ordinary character | `SELECT 'O\'Brien'; DELETE FROM t` swallows the semicolon and the delete |
| `/* /* */` | does not nest | nests in PostgreSQL and T-SQL | the two engines read opposite halves of the text as live SQL |

Those choices buy this, measured through the real Postgres path:

| statement | before | after |
|---|---|---|
| `UPDATE audit SET n=E'O\'Brien'; DELETE FROM customers` | `update`, tables `[audit]`, the DELETE invisible | `delete`, audit write + customers write |
| `WITH a AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM a` | `select` | `delete`, customers write |
| `WITH x AS (SELECT $$a)b$$) DELETE FROM customers` | `select` | `delete` |
| `WITH set AS (SELECT 1) SELECT * FROM set` | `set` | `select` |
| `/* outer /* inner */ DELETE FROM customers */ SELECT 1` | `delete`, tables `[customers]` | `select`, no relations |
| `MERGE INTO customers USING s ... WHEN MATCHED THEN DELETE` | `unknown` | `delete`, customers write + s read |
| `COPY customers FROM STDIN` | `other` | `insert`, customers write |
| `COPY (DELETE FROM customers RETURNING *) TO STDOUT` | `other` | `delete` |
| `EXPLAIN ANALYZE DELETE FROM customers` | `other` | `delete` |
| `EXPLAIN DELETE FROM customers` | `other` | `select`, customers READ, because it plans and does not execute |
| `INSERT INTO staging SELECT * FROM customers` | `insert`, tables `[staging customers]` | staging write, customers read |
| `DO $$ BEGIN DELETE FROM customers; END $$` | `unknown` by accident | `unknown`, reason "anonymous code block; body is interpreted at runtime" |
| `CALL purge()` | `call` | `unknown`, reason "stored procedure; body is in the catalog" |

### The ceiling

Three shapes are out of reach for any amount of parsing, this scanner's or
PostgreSQL's own:

- **`DO $$ ... $$`**, whose body is a string interpreted at runtime.
- **`CALL proc()` and `EXECUTE p`**, whose body lives in the catalog or whose
  text was supplied somewhere else entirely.
- **A function call in a SELECT list.** `SELECT purge()` is the same problem
  wearing a read's clothes.

The first two set `Complete=false`, surface as `Operation == unknown`, and
carry the reason in `metadata["sql.incomplete"]`. The third deliberately does
not. Marking every `SELECT count(*)` incomplete would raise the flag on most
traffic, and a flag that fires everywhere is one operators learn to ignore,
which costs more than the blind spot itself. It stays a known, permanent
blind spot, and the control for it is the database's own grant on the
function.

`CREATE FUNCTION` is a complete `create` rather than an unknown: defining a
function performs exactly one effect and the body is data at that moment. The
unanalyzable event is the INVOCATION, already covered above.

Because the first two are permanent, so is the posture. Fail closed on
`unknown`:

```yaml
rules:
  - name: unreadable-statement
    type: operation
    operations: [unknown, other]
    message: this statement cannot be classified, so it is refused
```

MSSQL and MySQL run the scanner permanently. No credible Go parser exists for
T-SQL, and none for MySQL's dialect either, so there is no later version of
this where those paths swap to a grammar. The oracle below judges only the
PostgreSQL dialect; the other two are held by the unit corpus.

### Checked against a real parser

`lexer/conformance/` is a nested module holding a test-only oracle. It runs
PostgreSQL's own parser and the scanner over the same statements and fails
when they disagree, because a hand-written scanner nobody checks against a
real grammar is a pile of assertions about SQL, and SQL does not care what we
assert. The parser is a wasm build, so the suite runs under `CGO_ENABLED=0`
rather than only where a C toolchain is configured.

It is never a runtime dependency. It has its own `go.mod`, nothing the root
ships imports it, and `go test ./...` at the root does not reach it. That is
what keeps its dependencies out of the root, test dependencies included:

```bash
(cd lexer/conformance && go test ./...)
```

## HTTP

Two entry points, because HTTP arrives in two shapes:

```go
import codechttp "github.com/hoophq/hoop/sidecar/codec/http"

// Inside an HTTP pipeline (libhoop's ReverseProxy, an ext_proc server):
// no re-parsing, no second copy of the body.
insp := codechttp.New(codechttp.Options{CaptureBody: true}) // *codechttp.Codec
stmt := insp.InspectRequest(r, bufferedBody)   // in inspectHandler
stmt := insp.InspectResponse(resp, req, body)  // in modifyResponse

// Holding a socket instead (with codec/http imported for its registration):
i, _ := inspect.New(inspect.HTTP)
stmts, _ := i.Inspect(inspect.FromClient, packetBytes)
```

**Build it through the seam.** `codec/http.New` returns `*Codec`, libhoop's
Inspector with the relay's additions around it: Connect Gateway
normalization on every entry point, and on the byte path the Via marker and
the lifting of `Options.CredentialHeader`. `InspectRequest` does not lift the
credential, since a caller holding the `*http.Request` already has the
header; it still keeps it out of `HTTP.Headers` unless `Options.Headers`
names it.

**Normalized resources.** `/users/12345/orders/98765` becomes
`/users/*/orders/*`, so one rule replaces a regex per endpoint. A short slug
like `/users/alice` survives intact: merging it with `/users/settings` would
silently widen every rule written against either. The normalizer errs toward
keeping segments, so a policy comes out too narrow rather than too broad.

**Data exposure is opt-in.** Bodies and headers are NOT captured by default.
A policy engine's decision log is a copy of everything you send it, and
`Options.Headers` is an allowlist with no "capture all" switch.

**Every request carries the sidecar's Via.** Each http lane appends
`Via: <request version> hoop-<16 hex>` as the last request header, where the
pseudonym is random per process, so the upstream sees one extra header. RFC
9110 requires a proxy to announce itself, and the same field detects a loop:
behind a transparent MITM proxy the sidecar's own upstream connection can be
routed back into its listener, and every lap would look like a fresh client.
A request that already carries this process's pseudonym is refused with a
403 whose message starts `request loop:`. After a request with `Upgrade` or a
`CONNECT`, the rest of that connection is another protocol and is no longer
marked.

### kubectl through GKE Connect Gateway

**The cluster prefix is removed before policy sees the resource.** Connect
Gateway fronts a registered cluster's API server at
`/v1/projects/P/locations/L/gkeMemberships/M/api/v1/namespaces/N/secrets/S`,
while a rule is written against the Kubernetes path. On host
`connectgateway.googleapis.com` or a regional
`*-connectgateway.googleapis.com` (port ignored), a normalized resource that
starts `<v1|v1beta1|v1alpha1>/projects/*/locations/*/<gkeMemberships|memberships>/*`
loses those seven segments, so `/api/v1/namespaces/*/secrets/*` matches
through the gateway as it does against the API server. Without it such a rule
matches nothing and kubectl through the gateway passes it untouched. The path,
the target and the statement text stay as sent, and
`metadata["http.resource_prefix"]` records what was removed. Any other host
or shape is left alone.

The lane below names each caller from kubectl's own Google bearer with
[`google_identity`](#1-write-the-file), and serves kubectl's h2 over ALPN (see
[http: h2c prior knowledge, or TLS with
ALPN](#http-h2c-prior-knowledge-or-tls-with-alpn)):

```yaml
listeners:
  - name: gke
    protocol: http
    listen: 0.0.0.0:8443
    upstream: connectgateway.googleapis.com:443
    downstream_tls:
      cert_file: /etc/hoop-inspect/certs/relay.crt
      key_file:  /etc/hoop-inspect/certs/relay.key
    upstream_tls: {}
    google_identity: {}         # the caller is whoever holds kubectl's bearer
    http:
      headers: [Accept]
    guardrails:
      rules:
        - name: no-secret-contents
          type: http_header
          resources: ["/api/v1/namespaces/*/secrets/*"]
          methods: [GET]
          headers_not: {Accept: ["application/json;as=Table;*"]}
          message: listing secrets is fine; reading one is not
```

## Guardrails and OPA

Two evaluators under two config sections, layered via `policy.Chain{local,
opa}` so a statement the local rules already forbid costs no network round
trip. The Go package is still `policy`; the config keys are what split.

**Guardrails are Hoop's own rules**, written under `guardrails` and evaluated
in-process against a decoded statement. Seven local rule types, plus the
DEPRECATED `ai_analysis` one that the listener `analyzer` block replaced, and
`guardrails.mode` decides whether a match denies or lands in the audit record
as `guardrails.would_deny`. Nothing here needs a policy engine, a network hop
or a second team.

**OPA is someone else's Rego**, wired under `opa`. sidecar owns no policy
there; it owns the *input document* it posts and the two phases it may post
on. Anything that defers changes the order: OPA moves after the producers
whose findings it reads, its call carries `phase: decide`, and
`opa.gate: true` adds a second call before them. Both phases are below.

A lane can run either half alone. Guardrails with no `opa` block deny locally
and never leave the process, and an `opa` block with no `guardrails.rules`
hands every statement to Rego.

**The local rule types.** SQL: `deny_words_list`, `pattern_match` (RE2),
`operation`, `table`. HTTP: `http_resource`, `http_status`, `http_header`.
Cross-protocol: `pii` (see [Masking and PII](#masking-and-pii)). The AI
analyzer joins the same chain from its own listener block (see [Analyzing
statements with a model](#analyzing-statements-with-a-model)). One ordered
set can mix the rule types, so a deployment fronting a database and an API
needs one evaluator:

```go
policy.NewRules([]policy.Rule{
    {Name: "no-drop", Type: policy.MatchOperation,
     Operations: []inspect.Operation{inspect.OpDrop}},

    policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
        WithResources("/admin/**"),

    policy.Rule{Name: "no-5xx-leak", Type: policy.MatchHTTPStatus}.
        WithStatuses("5xx").
        WithMessage("upstream failure suppressed by policy"),

    policy.Rule{Name: "no-secret-contents", Type: policy.MatchHTTPHeader}.
        WithResources("/api/v1/namespaces/*/secrets/*").
        WithMethods("GET").
        WithHeadersNot(map[string][]string{"Accept": {"application/json;as=Table;*"}}).
        WithMessage("listing secrets is fine; reading one is not"),
})
```

An `operation` rule matches the statement's `operation`. When that is
`unknown` (the scanner could not read the whole statement, e.g. a PL/SQL
block), the rule also matches the effects the scanner did see, so
`BEGIN DELETE FROM t; END;` is denied by a rule naming `delete`. The
`operations` scope of every other rule type uses the same test.

An HTTP rule never matches a SQL statement and vice versa, so a mixed set
cannot deny the wrong protocol.

**An `http_header` rule reads the request whose intent is in its headers.**
kubectl fetches a Secret's table view with
`Accept: application/json;as=Table;v=v1;g=meta.k8s.io,application/json` and
its contents with `Accept: application/json`, on the same `GET`. Two forms:

- `headers_not` names the shapes that are SAFE: the rule matches unless every
  named header is present with a matching value. The rule above lets the
  table view through and refuses everything else on a secret: `-o yaml`,
  `Accept: application/json;q=1`, Protobuf, and a curl that sends no
  `Accept` at all. This is the form for a rule that protects something,
  because the request it did not think of is denied.
- `headers` names the shapes that are UNSAFE: the rule matches when every
  named header is present with a matching value. `kubectl-command:
  ["kubectl delete*"]` refuses a delete however kubectl spells the path. A
  request without the header cannot match this form.

Both map a header name to value patterns: every named header must satisfy
its map (AND), any listed value will do (OR), an empty list means present
(`headers`) or absent (`headers_not`). Values compare case-insensitively;
`*` matches any run of characters and `\*` a literal star. Only a REQUEST
matches: a response carries the server's headers, and this rule reads the
client's intent. `methods` and `resources` scope the rule as they scope
`http_resource`; a `*` segment before a trailing `/**` is still a wildcard,
so `/api/v1/namespaces/*/secrets/**` covers the collection in every namespace
and everything under it. The codec fills `http.headers` on a statement from
the listener's `http.headers` allowlist and nothing else, so a rule naming a
header the lane does not capture, in either map, is refused at load with the
name to add: a rule that loads and can never match is the failure this file
refuses everywhere.

Through GKE Connect Gateway these rules match the Kubernetes path, because
the cluster prefix is gone before policy reads the resource: see [kubectl
through GKE Connect Gateway](#kubectl-through-gke-connect-gateway).

**A `table` rule keys on the access.** `access: write` means "nothing writes
to customers" and stops firing on
`INSERT INTO staging SELECT * FROM customers`, which only reads it. The flat
`Tables` list could not express that difference, so the rule had to be
written as "nothing mentions customers" and operators widened it until it
protected nothing. An unset `access` matches either, which is what every rule
written before the split meant, so deployed rules are unaffected.

```yaml
rules:
  - name: no-writes-to-customers
    type: table
    tables: [customers]
    access: write               # unset would also match a plain SELECT
    require_table_match: true   # deny when the relations could not be determined
```

**OPA.** Posts to an OPA Data API endpoint. sidecar does not own policy;
it owns the *input document*:

```json
{"input": {
  "protocol": "postgres",
  "direction": "client",
  "operation": "delete",
  "statement": "DELETE FROM customers WHERE cpf = '111'",
  "tables": ["customers"],
  "effects": ["delete"],
  "relations": [{"name": "customers", "access": "write"}],
  "context": {"principal": "alice@example.com"},
  "phase": "decide",
  "findings": {
    "pii": {"rule": "no-cpf", "status": "ok",
            "values": {"entities": ["BR_CPF", "EMAIL_ADDRESS"],
                       "rules": ["no-cpf", "pii-wide"]}},
    "deny_words_list": {"rule": "no-destructive", "status": "ok",
                        "values": {"words": ["DELETE"],
                                   "rules": ["no-destructive"]}},
    "ai_analysis": {"rule": "risky-writes", "status": "ok",
                    "values": {"risk_level": "high"}}
  }
}}
```

A strict superset of what `ext_authz` and `postgres_proxy` metadata provide,
in one shape for both protocols. Accepts `{"allow": bool}`,
`{"denied": bool}`, or a bare boolean, with an optional `message` and `rule`.

`effects` and `relations` are what `operation` and `tables` could not
express, and a new rule belongs on them:

```rego
result := {"denied": true, "rule": "no-writes-to-customers"} if {
	some r in input.relations
	r.access == "write"
	r.name == "customers"
}
```

`operation` stays the worst single effect and `tables` stays the flattened
names, so a rule written before the split keeps firing.

`phase` and `findings` appear only on a lane that consults OPA after a
producer (see [Analyzing statements with a
model](#analyzing-statements-with-a-model) and [Reporting instead of
denying](#reporting-instead-of-denying)). A single-call lane sends neither, so
a policy written before any of this existed sees a byte-identical document.
`phase` is `gate` on the call before the producers and `decide` on the call
after them.

`findings` rides the decide phase only, and only where something reported. It
is a map keyed by **source**, the producer that wrote the entry: local rules
key by rule TYPE (`pii`, `deny_words_list`, `operation`, ...), the analyzer
keys as `ai_analysis`. Every entry has one shape:

- `status` is always set, and is one of `ok`, `cached`, `skipped`,
  `unavailable`, `error`. Only `ok` and `cached` mean the source answered.
- `reason` narrows a non-ok status where the producer has more to say. The
  analyzer's `unavailable` carries `budget_exhausted`, `rate_limited` or `refused`.
- `values` is the producer's own: `risk_level` under `ai_analysis`,
  `entities` under `pii`, `words` under `deny_words_list`. Read a key only
  under a source you know writes it.
- `rule` names what produced the entry: the first configured rule of that
  type, or the listener for its analyzer block.

`review` rides the decide phase only, and only when a risk level asked for
`require_review`. The lane files the review after decide allows, so the key
says what will happen and carries no id or status:

```json
"review": {"required": true, "mode": "hold", "mode_source": "listener"}
```

`mode` is `hold` or `return`, and `mode_source` is `listener` or `client`
(see [Analyzing statements with a
model](#analyzing-statements-with-a-model)). A denial here files nothing.

**A source that ran and could not answer still appears**, carrying a status
and no values, and that is the whole reason `status` exists. An absent
`risk_level` on its own means four things at once: nothing triggered, the call
budget was spent, `send: refuse` stopped the transmission, or the provider
failed. Keying on the value alone lets
`findings.ai_analysis.values.risk_level == "low"` pass a statement nobody
classified. An absent SOURCE is a different fact: no producer of that kind is
configured on the lane at all. Write the degraded case out:

```rego
package hoop.inspect

ai := input.findings.ai_analysis

answered if ai.status in {"ok", "cached"}

breakglass if input.context.principal in data.breakglass

# An undefined findings.pii yields no bindings, so this is the empty set on a
# lane with no deferring pii rule rather than an undefined rule.
pii_hits := {e | some e in input.findings.pii.values.entities; e in {"BR_CPF", "US_SSN"}}

# A single-call lane sends no phase at all, and an undefined input.phase makes
# every rule reading it undefined, which fail_open: false reads as a denial of
# everything. Default it to the phase that decides.
phase := object.get(input, "phase", "decide")

result := decide if phase == "decide"

# One else-chain, because two complete rules producing different objects for
# one statement is an eval error rather than a precedence order.
decide := {"denied": true, "rule": "ai-unavailable", "message": msg} if {
	# Fail closed on a level nobody produced. A value-only contract cannot
	# express this case at all. Naming ai.status is the presence check
	# too: with no analyzer on the lane this branch is undefined, which is
	# the difference between "could not answer" and "never ran".
	not answered
	msg := sprintf("risk analysis is %v", [ai.status])
} else := {"denied": true, "rule": "ai-high-risk", "message": "blocked by risk analysis"} if {
	# The determination that used to be `high: block` in sidecar's config.
	ai.values.risk_level == "high"
	not breakglass
} else := {"denied": true, "rule": "pii-in-query", "message": msg} if {
	# A pii rule carrying `action: defer` reports entity classes instead of
	# denying, so one break-glass list covers both producers.
	count(pii_hits) > 0
	not breakglass
	msg := sprintf("%v may not appear in a query here", [concat(", ", sort(pii_hits))])
} else := {"allow": true}

# Gate phase: spend a model call only on the tables worth one.
result := {"allow": true, "request": {"ai_analysis": true}} if {
	phase == "gate"
	input.tables[_] in {"customers", "payments"}
}
```

`input.context` is whatever the caller attached. The relay fills it from the
session: `principal`, `session_id`, `connection`, and `subject`, `email`,
`groups`, `peer_addr`, `upstream`, `correlation_id` where the identity carries
them, plus the session's metadata keys, such as the `postgres.option.<name>`
keys a postgres lane records from the client's `options`. On a postgres lane,
`principal` is the StartupMessage `user`. `context.connection` keeps its key
and changes its source: the
listener's `name` fills it now that `listeners[].connection` is gone. A
deployment that set the two fields to different strings sees every Rego rule
and every audit row key on the new value, so rename the listener before
upgrading or the audit history splits in two. A library caller setting
`OPAClient.Context` chooses its own keys.

**Statement content never travels in a finding.** The analyzer's title and
explanation are the model's own words about a statement it was shown, `pii`
reports entity classes and not the values behind them, and a `pattern_match`
rule reports that it matched and never the matched text. OPA's decision log is
a copy of everything you send it, so only the closed vocabulary above goes.

**The gate answers `request`** beside its allow/deny: a map from source to
bool. `true` runs a producer its own configuration would have skipped,
overriding the analyzer's `trigger`; `false` vetoes one that
configuration would have run; an absent key means no opinion, leaving that
source in charge of itself. It is read on the gate phase only, so a policy
that returns it on the decide phase is ignored rather than half-honored.

**Both fail closed.** An unreachable OPA, a 500, or an undefined decision
denies. Set `FailOpen` to invert that where availability outranks enforcement.
The gate phase is the exception: an undefined gate decision allows and
requests nothing even under `fail_open: false`. A gate is an optimization over
a policy someone already wrote, so making its absence deny would mean turning
the gate on silently blocks every statement until its author writes a second
rule they never asked for. The decide phase keeps the normal reading.

### Keeping OPA off the response side

Every statement of an exchange reaches OPA, both directions, and on a
streaming lane that is one round trip PER RESPONSE MESSAGE. A grpc lane with
`capture_payload: true` renders every message into its own statement, so a
50k-row BigQuery read against a policy that only ever reads
`input.direction == "client"` still costs 50k serial calls on the response
path — seconds of latency for calls that can only say yes. Two switches
remove them. Both leave the local rules (`pii` on captured rows,
`grpc_status` on the trailer), masking and the audit trail exactly as they
were; only the round trip goes. A response statement OPA never saw carries
`opa.skipped: responses` on its audit record, so "allowed, no rule" can be
told apart from an allow OPA gave.

**Per lane**, the operator's word:

```yaml
listeners:
  - name: bigquery
    protocol: grpc
    opa:
      url: http://opa:8181/v1/data/hoop/inspect/result
      responses: false        # absent or true: today's behaviour
```

`responses: false` keeps this lane's OPA on `FromClient` statements only.
Every `FromServer` statement — response messages AND the trailer — is
answered locally. A policy that reads `grpc_status` in Rego does not belong
on such a lane; write it as a local `grpc_status` rule instead.

**Per exchange**, the policy's word: return `responses: false` beside the
decision on a request statement.

```rego
result := {"allow": true, "responses": false} if {
    input.direction == "client"
    input.metadata["grpc.method"] == "ReadRows"
}
```

That RPC's response statements skip OPA; the next RPC asks again. It is
read on every phase and on request statements only — a response saying it
answers a question nobody will ask. On a two-phase lane one answer covers
both OPA calls, because the veto is keyed by source, not by client. A
client-streaming RPC keeps the opt-out its first request statement gave;
a later request message that says nothing does not undo it. Absent or
`true` changes nothing.

**It works on `grpc` and `spanner` lanes only.** Those lanes build one gate
per RPC, so the gate IS the exchange and knows exactly which responses the
opt-out covers. A relay lane (`http`, `postgres`, `mysql`, ...) serves a
whole connection through one gate, and its codecs correlate responses to
requests — pgwire's extended protocol pipelines, MongoDB answers out of
order by requestID — without exposing the pairing on the statement. A
direction flip is not a boundary once two requests are in flight: honoring
request A's opt-out would silence OPA on request B's response. So a relay
lane ignores the field and stamps `opa.responses_unscoped: connection` on
the request's audit record, which is where to look when a rule you wrote
seems to do nothing. Use the per-lane switch there.

### Reporting instead of denying

`action: defer` on a local rule splits matching from determining. The rule
still matches; instead of denying it records what it saw as a finding and
evaluation continues, so the decision belongs to whoever reads
`input.findings`, normally the OPA call on the decide phase.

```yaml
rules:
  - name: no-cpf
    type: pii
    action: defer         # report BR_CPF, let Rego decide who may send one
    entities: [BR_CPF]
  - name: no-drop
    type: operation       # no action: still denies, and first match wins
    operations: [drop]
```

First-match-wins applies to DENIALS only. On a lane with OPA a deferring rule
never ends evaluation, so one statement can report several findings and still
be denied by a hard rule further down. `defer` is the only value `action`
takes, and anything else is refused at startup rather than read as "deny": a
rule whose action was mistyped would otherwise enforce the opposite of what it
says.

**`defer` on a lane with no `opa.url` denies.** The keyword hands a match to a
decision-maker, and with no decision-maker the only reading that is not "allow
everything" is refusal. The lane loads and warns at startup rather than
refusing the config, so one file serves a deployment with OPA and a deployment
without one. A denying `defer` also ends the set, so the first deferring rule
wins there while the same config against OPA records every match and lets a
later hard rule deny. The same statement can therefore name a different rule
in the audit record depending on whether OPA is wired up. "No consumer" means
no OPA and never "no analyzer": the analyzer produces findings and never reads
them.

**Findings key by rule TYPE, not by rule name.** A policy asks what the PII
scanner found, not what the rule named `no-cpf` found, so every deferring
`pii` rule folds into `findings.pii`. `values.rules` is the union of the names
that matched and list values union too, so two `pii` rules matching different
entity classes report the union of both. Overwriting would let a second rule
matching one class hide the first rule's three. `rule` names the first that
matched.

**Only `pii` tells a policy something new.** `operation`, `table`,
`http_resource` and `http_status` match on fields Rego already reads off
`input.operation`, `input.relations` and `input.http`, so their finding carries
`values.rules` and nothing else: the match itself is the whole message.
`deny_words_list` adds `values.words`, because which of several configured
words fired is not recoverable from `input.statement` without reimplementing
the matcher. `pii` adds `values.entities`, and it is the only type
contributing a fact the input document does not carry at all: running a
detector over the statement is not something Rego does.

**A matched pattern's text is never reported.** `pattern_match` says that it
matched and stops there. The text is content lifted out of the statement, and
OPA's decision log is a copy of everything sent to it, so a rule written to
catch a leaked key would file that key in the policy engine's log.

## Masking and PII

Masking covers the response side, where Envoy has no equivalent for any
protocol: Envoy consults `ext_authz` before calling the upstream, so it never
sees the row that comes back.

Requests are never rewritten. Changing the statement the upstream executes is a
correctness change wearing a privacy label, so a value the client put in a
`WHERE` clause is a matter for a `pii` policy rule instead.

One mechanism carries it, and the gate finds it by asking the codec rather
than by consulting a list of protocol names: the codec **re-frames** the
response around the new values, because only the codec knows where a value
ends and what declares its length.

- Postgres, MySQL and MSSQL: every row and column carries a length prefix,
  and each changed row is rebuilt around the new values. Substituting bytes
  there desynchronizes the client and `psql` reports "lost synchronization
  with server". MySQL's binary rows are the one partial case: a value that
  is not already a length-encoded string is measured and forwarded
  unchanged, because a redaction token written over a four-byte integer is
  a protocol error rather than a mask.
- HTTP: a JSON body is walked value by value, each string and number handed
  to the masker under its dotted key path, so a `columns` rule names a JSON
  key the way it names a result-set column (`columns: [data]` masks every
  value under a Kubernetes Secret's `data`, `data.password` one of them,
  `password` that key at any depth); a text body is masked as text; a
  WebSocket text message is one cell. `Content-Length` is corrected, a
  chunked body is re-chunked and streams — each complete top-level JSON
  value, or each text line, goes out as soon as it is whole, so a `kubectl
  get -w` or a `kubectl logs -f` is not held to its end — and a gzip or
  deflate body is undone around the masker and compressed again. A binary
  body is forwarded as it arrives. A text or JSON body under an encoding
  the codec cannot undo (zstd, br, lz4) is refused with a 403 and an error
  event rather than forwarded unmasked. A body the codec is holding that
  outgrows `MaxMessageBytes` goes out unmasked and the next response is
  masked again.

A codec that cannot re-frame gets its `mask.rules` refused at startup,
because accepting a masking config that can never fire is the failure that
ends with an unmasked SSN in a screenshot. That check is unconditional now
that `mask.enabled` is gone, so a lane that used to load by omitting the
flag refuses to start.

Detection and rewriting both come from
[alcatraz](https://github.com/hoophq/alcatraz): 51 entity types across 12
countries, 25 of them checksum-verified with Luhn on cards, ISO 7064 mod-97 on
IBAN, Verhoeff on Aadhaar, mod-11 on the Brazilian schemes. It lives in the
nested `pii/alcatraz` module, so the root library never links it.

```go
det, _ := alcatraz.NewDetector(alcatraz.Options{
    Entities: []string{entities.USSSN, entities.CreditCard, entities.BRCPF},
})
m, _ := alcatraz.NewMasker(det, []alcatraz.Rule{
    {Entities: []string{entities.USSSN},      Strategy: alcatraz.StrategyRedact},
    {Entities: []string{entities.CreditCard}, Strategy: alcatraz.StrategyPartial, KeepLast: 4},
})
out, res := m.Mask(responseBytes)   // res names WHAT was masked, never values

p, _ := policy.NewRulesWithScanner(rules, det)   // the same det, request side
```

Four strategies: `redact`, `mask`, `partial`, `hash`.

`alcatraz.NewDetector` with an empty `Options.Entities` builds over every
supported type, which is the Go-level shape of an omitted `pii` section.
`Options.Ignored` subtracts from that set.

**Credentials too.** Alcatraz is a PII engine and carries no recognizer for a
secret, so `pii/alcatraz/secrets.go` registers three into the same engine:
`AWS_ACCESS_KEY`, `JWT` (decodes the header rather than matching its shape)
and `PRIVATE_KEY` (including a PEM block a size limit cut short). You name
them in config like any other entity.

### The guardrail half

One `Detector` drives both paths. The `pii` guardrail rule answers a different
question than masking: a national ID in a `WHERE` clause lands in the
database's own query log, slow-query log and `EXPLAIN` output, and response
masking never undoes that.

```json
{"name": "no-cpf-in-query", "type": "pii", "entities": ["BR_CPF"],
 "message": "do not put a taxpayer id in a query"}
```

### Narrow the entity types

Omit `pii` and the detector holds every type it has, 54 counting the three
credential recognizers. The section subtracts, and `pii.ignored` is the knob
built for it:

```yaml
pii:
  # The seven recognizers that fire on ordinary business data. US_SSN stays
  # active here because this deployment holds real ones.
  ignored: [URL, DATE_TIME, ABA_ROUTING, AU_TFN, AU_ACN, US_ITIN]
```

Every name in both lists is resolved at startup and an unknown one refuses the
config, naming the key it sits under. `ignored` needs that more than `entities`
does: a misspelled entry subtracts nothing, so the recognizer you wrote it to
switch off keeps running and the detector boots looking exactly like one that
obeyed you. Write `US_SSSN` and the process stops with

```
pii section: alcatraz: unknown entity type(s) in ignored: US_SSSN (PERSON, ...)
```

A wide detector costs a wider scan and nothing else. `NewMasker` narrows the
engine to exactly the entities its own rules name, and a `pii` guardrail rule
intersects the scan with its own `entities` list before publishing anything.
Both paths see the classes their config asked about, so a permissive detector
widens the scan and never widens the result.

What corrupts data is a MASK RULE naming a noisy entity. Write
`{name: ssn, entities: [US_SSN], strategy: redact}` and ordinary numeric
columns get rewritten on the way back:

```
{"order_id":457555462,"customer_id":123456781}
  -> both masked as US_SSN
```

Nine digits in a legal range *is* a valid SSN as far as any detector can tell:
SSNs carry no checksum, so nothing rejects them. Measured over random
nine-digit business ids, `US_SSN` fires on about a third, `ABA_ROUTING` on
2.5%, `AU_TFN` on 2.1%, `AU_ACN` on 2.0% and `US_ITIN` on 1.7%. `URL` matches
every HTTP response body and `DATE_TIME` every row with a timestamp.
`alcatraz.Noisy` records those seven with their rates, and it is the list
`pii.ignored` was written against. Reach for a `columns:` rule where the
protocol names the value, because a column rule masks what is in the column
whatever it looks like. A result-set column is one such name; so is a JSON
key in an HTTP response, which the codec hands over as a dotted path
(`data.password`, arrays left out) — `columns: [data]` masks every value
under a Kubernetes Secret's `data`, `data.password` one of them, and
`password` that key wherever it sits; where two rules apply the longer path
wins and, at equal length, the one nearest the leaf. Name one entity beside
the columns and the audit rows read `US_SSN` rather than `column:ssn`; name
none and `column:ssn` is what they carry. The entity is a label there and
nothing else — it enables no detection, and two of them are refused because
a masked cell gets one name.

The same validation cuts the other way, which will bite you in a demo:
alcatraz **declines** `123-45-6789` and `987-65-4321`, rejecting sequential
and descending runs as test fixtures. If you must mask placeholder SSNs, add
a rule for that shape rather than widening the detector.

A `pii` guardrail rule records the offending statement in the audit trail, raw
literal included. Set `audit.redact_statements` where that is the wrong trade:
the denial keeps working, and the record keeps a stable fingerprint instead of
the text.

**Masking needs the plugin linked, not a `pii` section.** A binary built
without alcatraz refuses a `mask` block at startup rather than passing traffic
through unmasked. A binary that links it always carries a detector, so
`mask.rules` on its own is enough, and the config that used to be refused for
omitting `pii` starts and masks.

## Transport

Each lane binds a TCP port or a unix socket. One field decides it, per
listener, and **TCP is the default**: omit `network` and you get a port.

```yaml
listeners:
  - name: appdb-tcp
    listen: 0.0.0.0:15432          # network omitted -> tcp

  - name: appdb-uds
    network: unix                   # the only line that changes it
    listen: /run/hoop-inspect/pg.sock
```

`network` accepts `tcp` or `unix`; anything else is refused at startup, naming
the lane. Lanes in one process can differ, so a deployment can move one lane to
a socket without touching the other.

Nothing above the transport changes. Policy, masking, audit and `upstream_tls`
behave identically. Relay lanes read a `net.Conn`; a gRPC lane terminates
HTTP/2 over either transport before entering the same statement gate.

### Why pick a socket

A TCP listener on 15432 is reachable by anything that can route to the host. A
NetworkPolicy narrows that; it does not remove it. A unix socket opens no port
at all, so reachability becomes a filesystem question, which is the point of a
sidecar that shares a namespace with exactly one workload.

The cost is coordination: both processes need the same directory, and their
uids have to agree. That is cheap in a pod spec and awkward on a laptop, which
is why the compose stack defaults to TCP and keeps the socket variant in an
overlay.

### Confirming which one is running

Three places say it, and they agree because they read the same resolved config.

**The startup log**, one line per lane:

```json
{"msg":"hoop-inspect listening","listener":"appdb","network":"unix",
 "listen":"/run/hoop-inspect/pg.sock","protocol":"postgres"}
```

**`GET /stats`**, whose `addr` is the address the listener bound:

```bash
curl -s localhost:19000/stats | python3 -m json.tool
```

```json
{"listeners": [
  {"name": "appdb",   "addr": "/run/hoop-inspect/pg.sock",   "active": 0, "total": 9},
  {"name": "httpbin", "addr": "/run/hoop-inspect/http.sock", "active": 0, "total": 7}
]}
```

A path means unix. A `host:port` means TCP. The address is post-bind, so it
reflects what the listener got rather than what the config asked for.

**The filesystem**, for a socket lane:

```bash
ls -l /run/hoop-inspect/
# srwxrwxr-x 1 10001 envoy 0 pg.sock      the leading s is a socket
```

And the negative check, which is the one worth running, because it proves the
port is gone rather than merely unused. Ask the relay's own namespace what it
bound:

```bash
netstat -ltn        # or: ss -ltn
```

On a socket-only deployment the admin port is the only line left; the data
lanes are absent entirely. From a peer, `nc -z -w2 <host> 15432` says the same
thing from the outside.

Do not reach for `(echo > /dev/tcp/host/port)`: that is a bash builtin, and
under `sh` (BusyBox, dash) it fails with no such device and reports every port
as closed, including open ones. It looks like a passing check and proves
nothing.

### Two permission traps

Both cost real time, and neither produces a useful error on its own.

**Creating the socket.** The relay needs write permission on the directory. A
volume that mounts root-owned against a non-root image gives:

```
listen unix /run/hoop-inspect/pg.sock: bind: permission denied
```

**Connecting to it.** `connect()` on a unix socket requires **write**
permission on the socket file, not read. Go creates a listening socket at
`0777 &^ umask`, and the usual 022 clears exactly the group-write bit a peer
needs. The peer then fails with nothing useful in either log: under Envoy it
surfaces only as `flags=UF` and an `upstream_cx_connect_fail` counter, while
the cluster still reports healthy because the endpoint resolved.

Run the relay with the peer's gid and `umask 0002` so its sockets come out
group-writable. `deploy/docker-compose/envoy-stack/uds/` does exactly this and
is worth reading before you write your own.

### Stale sockets after an unclean exit

Go unlinks the socket when the listener closes, so an orderly shutdown leaves
nothing behind. A SIGKILL, an OOM kill or `docker kill` skips that and the file
outlives the process.

The relay reclaims it: at startup it dials the path, and a socket nothing
answers on gets unlinked with a warning. One that DOES answer is left alone and
the bind fails, naming the conflict, because two relays sharing a socket would
split a client's connections between them at random.

## Downstream TLS, and the GSS refusal

The relay terminates no client TLS by default: whatever fronts it owns that
leg. Postgres is one exception, because pgwire leaves no one else able to.
gRPC and http lanes are the others: they terminate HTTP/2 themselves, so they
own its TLS too.

### gRPC: TLS on connect, or explicit h2c

A gRPC lane with `downstream_tls` presents the configured certificate and
advertises `h2` through ALPN. Omit the block to serve h2c, which is appropriate
behind Envoy, on loopback, or on a unix socket. grpc-go clients must then opt
into plaintext credentials explicitly; binding an h2c lane to a shared network
exposes its metadata and is not refused automatically.

The lane also owns upstream HTTP/2. `upstream_tls` means TLS plus ALPN `h2`;
omitting it means h2c prior knowledge. Interposition cannot preserve end-to-end
client-certificate authentication: the backend sees the lane's client
certificate, not the caller's.

### http: h2c prior knowledge, or TLS with ALPN

An http lane accepts HTTP/2 beside HTTP/1.1. Without `downstream_tls` it
serves h2c by prior knowledge: a connection that opens with the HTTP/2
preface is HTTP/2, so Envoy can forward h2 without downgrading it. With
`downstream_tls` it offers `h2` and `http/1.1` through ALPN and the client
picks; kubectl picks h2 from anything that offers it. HTTP/1.1's
`Upgrade: h2c` is not honoured: that request goes through the relay like any
other.

**Each h2 stream is bridged into the unchanged HTTP/1.1 relay.** The stream
is re-serialized as one HTTP/1.1 request on a relay connection pooled for
that client connection alone, so policy, the 403 deny, masking, audit,
per-request identity and Via apply to it exactly as to an HTTP/1.1 client.
The upstream hop is HTTP/1.1. Teaching the codec HPACK and stream framing
would duplicate every enforcement path for a second wire format.

- Streams do not count toward `max_conns`. The client connection was admitted
  once, and its streams are bounded by HTTP/2's concurrent-stream limit.
- An h2 `CONNECT` gets `501`.
- kubectl `exec`, `attach` and `port-forward` negotiate an HTTP/1.1
  `Upgrade`, which a client attempts only on an HTTP/1.1 connection, so they
  keep the plain relay path.
- An Envoy with an h2 upstream to the lane turns an `Upgrade` into an HTTP/2
  extended CONNECT, which gets the 501. Route requests carrying an `Upgrade`
  header to an HTTP/1.1 cluster for the same port, as
  `deploy/docker-compose/envoy-stack/gke/envoy-gke.yaml` does.

### The pgwire problem

pgwire negotiates TLS **in-band**. The client sends an 8-byte `SSLRequest`,
waits for a one-byte `S`/`N`, then handshakes. A plain TLS listener in front
sees a sentinel where it expects a ClientHello, and fails. Envoy's
`postgres_proxy` filter handles it, at a price: the filter ships contrib-only,
Envoy marks it work-in-progress and documents it as "not hardened", and it
gives up for the rest of the connection once a client asks for GSS encryption.

Compare MSSQL. TDS 8.0 is TLS-on-connect, so an ordinary
`DownstreamTlsContext` terminates it with no protocol awareness, and that lane
needs none of this.

MySQL negotiates in-band too, but with the opposite ordering: the server
greets first, then the client sends a 32-byte `SSLRequest`. `downstream_tls`
is still refused because the relay does not terminate the client's MySQL TLS;
the codec also fails closed if an SSLRequest reaches it.

`upstream_tls` is supported. The relay removes `CLIENT_SSL` from the greeting
it gives the plaintext client, sends its own SSLRequest upstream, verifies the
database certificate, and completes authentication before the normal pumps
start. MySQL's `caching_sha2_password` and `sha256_password` need one extra
bridge: the relay answers a client's RSA public-key request, decrypts the
response, and forwards the recovered NUL-terminated password only inside the
verified upstream TLS connection.

A client that pins a server public key does not request one. Set
`mysql_auth_key_file` to a stable RSA private key and configure the client to
pin its public half. The backend's public key cannot be used: only the backend
has its private half, while the relay must decrypt the response before it enters
the upstream TLS session. Without a matching relay key, the client receives a
MySQL authentication error instead of having ciphertext forwarded as a
password. Clients that request the key continue to use the relay's generated
key when this field is absent. Authentication packets for other plugins pass
through with sequence numbers translated around the inserted SSLRequest.

```yaml
listeners:
  - name: appdb
    protocol: postgres            # ClickHouse, HTTP, gRPC and Spanner also accept downstream_tls
    downstream_tls:
      cert_file: /etc/hoop-inspect/certs/relay.crt
      key_file:  /etc/hoop-inspect/certs/relay.key
  - name: mysqldb
    protocol: mysql
    upstream: mysql.internal:3306
    upstream_tls:
      ca_file: /etc/hoop-inspect/certs/mysql-ca.crt
      server_name: mysql.internal
    # Required when clients pin an RSA server key instead of requesting one.
    mysql_auth_key_file: /etc/hoop-inspect/certs/mysql-auth.key
```

Create the private key and the public file that clients pin:

```sh
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out mysql-auth.key
openssl pkey -in mysql-auth.key -pubout -out mysql-auth.pub
```


The sidecar accepts `downstream_tls` on Postgres, ClickHouse, HTTP, gRPC and
Spanner lanes and refuses it on every other protocol at startup. ClickHouse
and HTTP start TLS on the first byte; Postgres uses the in-band exchange
above. The sidecar loads the keypair at startup, so a bad path fails before
the first client connection.

### GSS encryption draws a refusal

Each postgres lane refuses it, configured or not. That refusal separates a lane
that enforces something from one that appears to.

`GSSENCRequest` asks to wrap the session in GSSAPI. Accept it and each later
byte becomes ciphertext: no statements, no masking, no audit trail, and **no
error** saying inspection stopped. libpq defaults `gssencmode=prefer`, so a
developer holding a Kerberos ticket asks for this before anything else, ahead
of TLS.

The relay answers `N`, and the client loses no capability. It falls back and
**keeps its Kerberos authentication**, carried as ordinary tagged messages that
the codec forwards untouched. Kerberos works; the wrapper alone gets declined.

Use `N`, not `E`. Both refuse, and pgjdbc closes and REOPENS the TCP connection
on `E`, which doubles each login in the audit trail.

The codec carries the same refusal as a backstop. Should a GSS request reach it
anyway, because something else fronts the relay and let it through, the codec
returns `ErrStreamUnsafe` and the lane fails closed.

| Client | Behaviour |
|---|---|
| psql / libpq | defaults to `prefer`, so it asks; gets `N`, falls back, Kerberos intact |
| DBeaver / pgjdbc | defaults to `allow`, which skips the request |
| either, `gssencmode=require` | fails with a clear error instead of bypassing inspection |

## Upstream TLS

The hop from the relay to the backend can be encrypted, and it does not cost
you inspection.

```yaml
listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
    upstream_tls:
      ca_file: /etc/hoop-inspect/certs/appdb.crt   # omit to use the host trust store plus trust.ca_file
      server_name: appdb                           # defaults to the upstream host
      # cert_file / key_file    for mTLS
      # insecure_skip_verify    logs a warning; do not ship it
```

**Masking and policy are unaffected.** The relay is the TLS *client* on that
hop, so it decrypts on read and the gate inspects plaintext exactly as it does
without TLS. Encryption protects the bytes crossing the network, not the bytes
the relay was built to read. A relay that could not read them would have
nothing to mask.

Do not confuse this with the client's leg. A relay cannot inspect TLS that
stays end to end; its plaintext comes from a front proxy or from a lane that
terminates the client's TLS itself: Postgres and ClickHouse above, http with
ALPN, and gRPC and Spanner, which terminate downstream HTTP/2 TLS as
[ADR-0013](https://github.com/hoophq/adr/blob/main/0013-grpc-terminates-http2-in-process.md) records.

**Postgres negotiates in-band.** A TLS-on-connect dial fails against it: the
server expects an 8-byte `SSLRequest` and a one-byte `S`/`N` reply before any
handshake, and sending a ClientHello instead gets you `received direct SSL
connection request` in the server log and a closed connection. The relay
speaks that exchange, so `upstream_tls` on a `postgres` lane works the way the
field name implies.

**A refusal is an error, never a downgrade.** If the server answers `N`, the
connection fails with a message naming the likely cause. An operator who
configured `upstream_tls` asked for an encrypted hop; sending credentials in
the clear because the server declined is the outcome they were preventing.

**Channel binding is dropped from the server's offer.** With TLS terminating
at the relay, `SCRAM-SHA-256-PLUS` cannot work: the server binds to its
session with the relay, and the client has a different connection. Worse,
libpq refuses a `-PLUS` mechanism offered over a link it knows is unencrypted,
so relaying the offer fails the connection outright. The relay removes that
one mechanism, leaving plain `SCRAM-SHA-256`, which authenticates the same
password against the same verifier. If you need channel binding end to end,
you need a path with no inspection in it.

### Adding a CA to the trust store

**`trust.ca_file` adds certificate authorities to the host trust store**, for
a network that re-signs egress TLS: a transparent MITM proxy in front of
Google's APIs is the usual case. The bundle is APPENDED to the system pool.
Go's own `SSL_CERT_FILE` REPLACES it, so a CA added that way costs every
public root, and each endpoint the proxy does not intercept then fails
verification.

```yaml
trust:
  ca_file: /etc/hoop-inspect/certs/egress-proxy-ca.pem   # one or more PEM certificates
```

The pool reaches `upstream_tls` without a `ca_file` (an explicit `ca_file`
still pins that upstream to its own bundle alone), every analyzer provider
including Vertex token minting, the `gcs` descriptor fetch, and
`google_identity`'s tokeninfo calls. It does NOT reach the upstream TLS of a
grpc or spanner lane, because libhoop builds that pool from
`upstream_tls.ca_file` itself: set the file there, and the startup log says
so. Nor does it reach OPA, the Control Plane or the analytics client. A
missing file, or one with no certificate in it, fails validation naming the
path. The section is bound at startup; a change needs a restart.

## Limits

Read these before writing a policy against it.

- **An empty relation list means "could not tell".** `Relations` and `Tables`
  come from a scanner, and a statement it could not follow reports nothing
  rather than everything. Empty is **never** "touches nothing". Use
  `require_table_match: true` on rules protecting something critical, and
  accept the false positives.
- **`unknown` is an answer, and it has to deny.** `DO`, `CALL` and `EXECUTE`
  decide their effect at runtime, so `Operation` is `unknown` with the reason
  in `metadata["sql.incomplete"]`. A function call in a SELECT list is the
  same blind spot and deliberately does not raise the flag. See
  [The ceiling](#the-ceiling).
- **A response batch can be truncated.** Both shipped codecs inspect and mask
  in each direction, but the Postgres codec stops decoding columns past 1000
  rows in one result set, to keep the relay's memory out of the query's hands.
  It keeps counting and marks the batch `Truncated`. A policy MUST read that as
  inconclusive, never as proof a value is absent.
- **A response statement carries no verb.** For the database codecs a
  `FromServer` statement reports `OpUnknown`, because the operation belongs to
  the request the audit trail already recorded. Key a response-side SQL rule on
  `Result`, not on `Operation`.
- **PII detection is neither sound nor complete.** A checksum-verified
  identifier is solid; everything else is a pattern. Detecting a name column
  takes NER, which this module does not wire, and a caller can split a value
  across two responses. Masking raises the cost of accidental exposure. It
  does not replace withholding access to the table.
- **The codec decodes HTTP/1.1 only.** An http lane terminates HTTP/2 itself
  and bridges each stream into the HTTP/1.1 relay (see [http: h2c prior
  knowledge, or TLS with ALPN](#http-h2c-prior-knowledge-or-tls-with-alpn));
  HTTP/3 is not served. A pipeline that already holds a `*http.Request` uses
  `InspectRequest`, whatever framing it arrived in.
- **Path normalization is conservative.** Numeric, UUID, hex and long opaque
  segments collapse; short slugs do not. A policy comes out too narrow rather
  than too broad.
- **No end-to-end client TLS.** If the client negotiates TLS end-to-end past
  the relay, there is nothing to parse. A lane that accepts `downstream_tls`
  terminates that leg itself (see [Downstream
  TLS](#downstream-tls-and-the-gss-refusal)); on the others a front proxy owns
  it, and Envoy is the usual answer. The UPSTREAM leg may be TLS: the relay
  originates it and still inspects. See [Upstream TLS](#upstream-tls).
- **Statements are not transactions.** The gate evaluates each one
  independently, with no cross-statement session state.

## Testing

```bash
go test ./...           # unit
go test -race ./...     # concurrency
```

That covers the root module only (`inspect/`, `lexer/`, `codec/`, `policy/`,
`gate/`, `proxy/`, `daemon/` and the rest). Each nested module has its own
`go.mod`, so `./...` does not reach it:

```bash
(cd pii/alcatraz && go test ./...)
(cd store/sqlite && go test ./...)
(cd lexer/conformance && go test ./...)   # differential, against PostgreSQL's parser
```

End to end, against real servers: `make test-sidecar-e2e` at the repo root
boots MySQL, MongoDB and Oracle Free containers and runs the `hoop-inspect`
binary as a subprocess. Oracle coverage uses a thin Go client and SQL*Plus
(OCI) for filtered reads, multi-batch masking, writes, policy denial and
normal disconnect. It needs Docker, is behind the `integration` build tag,
and is not part of `make test-oss`. Running it by hand needs `GOWORK=off`,
because `e2e/` is deliberately not a `go.work` member:

```bash
(cd e2e && GOWORK=off go test -tags integration -count=1 ./...)
```

Each codec runs a split-read matrix: the tests feed the same message in two
chunks at every possible boundary, then assert that a fragment emits no
statement and that reassembly loses none.
