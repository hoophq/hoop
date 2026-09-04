# CLAUDE.md

## Project Overview

A Go workspace (`go.work`) for the hoop gateway, agent, and CLI.

- Product modules: `gateway/`, `agent/`, `client/`, `common/`, `tunnel/`.
- `sidecar/` adds seven more workspace entries (the module plus six nested ones). It was `hoopinspect/` until the CLI command became `hoop start sidecar`.
- `agentrs/` — Rust companion binary for RDP/TLS proxy workloads.
- `webapp/` — legacy ClojureScript SPA; see `webapp/CLAUDE.md` for its own conventions.
- `webapp_v2/` — React frontend that is replacing it.
- `libhoop` is not in this repo. It is one private module, `github.com/hoophq/libhoop`, resolved from the module proxy like any other dependency. Builds need `GOPRIVATE=github.com/hoophq/libhoop` and credentials for it. To work against a local clone, `make libhoop-dev`.

## Toolchain & Prerequisites

- Go >= 1.26, Rust + `cross` (for cross-compiled `agentrs`), Docker, Node/npm, Clojure/Java.
- PostgreSQL is the default state store (`POSTGRES_DB_URI`). `POSTGRES_DB_URI=pglite:///<data-dir>` starts the embedded PGlite instead (`gateway/pglite`).
- `golang-migrate` CLI for creating new SQL migration files.
- `swag` (v1.16.3) for regenerating OpenAPI docs.
- See `DEV.md` for full setup walkthrough.

## Architecture

```
┌────────┐  gRPC :8010   ┌─────────┐  gRPC :8010   ┌───────┐
│ Client │ ─────────────> │ Gateway │ <──────────── │ Agent │
│ (CLI)  │  Packet stream │  (API+  │  Packet stream│       │
└────────┘                │  gRPC)  │               └───────┘
                          │ :8009 HTTP/UI
                          └─────────┘
                               │
                          PostgreSQL
```

## Module Breakdown

### Client (`client/`)
- Entrypoint: `client/hoop.go` → `client/cmd/root.go` (Cobra CLI).
- Key commands: `connect.go`, `exec.go`, `login.go`, `run.go`, `start.go`, `proxymanager.go`, etc.
- User commands send packetized messages over a `Transport.Connect` bidirectional gRPC stream.
- Local proxy manager (`client/cmd/proxymanager.go`) opens local ports for protocol proxying (PG, SSH, etc).

### Gateway (`gateway/`)
- Entrypoint: `gateway/main.go` → `Run()`.
- **Startup order**: load config (`appconfig.Load`) → run DB migrations (`modelsbootstrap.MigrateDB` + `RunGolangMigrations`) → bootstrap org/auth → register transport plugins → start proxy servers → start gRPC (`:8010`) + HTTP API (`:8009`).
- gRPC server: `gateway/transport/server.go` — implements `PreConnect`, bidirectional `Connect`, `HealthCheck`.
- HTTP API: `gateway/api/server.go` — Gin framework, serves static UI and the REST API under the `API_URL` path (default `/` and `/api/*`).
- Route registration: `r.<METHOD>(path, [RoleMiddleware], r.AuthMiddleware, [api.AuditMiddleware()], [analytics tracking], handler)
- Role middleware: `AdminOnlyAccessRole`, `AdminAndAuditorAccessRole`, `ReadOnlyAccessRole`, or none (standard). See `gateway/api/apiroutes/roles.go`.
- Auth middleware: `gateway/api/apiroutes/auth.go`.
- Audit middleware: `gateway/api/middleware.go`.
- Database: `gateway/models/` (GORM-based, one file per entity).
- `gateway/storagev2/` — despite the name, not storage: the request-scoped `Context` (`APIContext` + Segment) that handlers read with `ParseContext(c)`, plus `clientstate/`.
- Services: `gateway/services/` — business logic layer.

### Agent (`agent/`)
- Entrypoint: `agent/main.go` → `Run()` or `RunV2()` (embedded/DSN mode).
- Pre-connect loop (`PreConnect` RPC) then long-lived `Connect` stream with exponential backoff/reconnect.
- Packet dispatch: **type-driven** in `agent/controller/agent.go` → `processPacket()` switch statement.
- Protocol handlers: `postgres.go`, `mysql.go`, `mssql.go`, `mongodb.go`, `oracle.go`, `ssh.go`, `tcp.go`, `httpproxy.go`, `mcpproxy.go`, `mcpstdio.go`, `ssm.go`, `terminal.go`, `terminal-exec.go`.
- System operations: `agent/controller/system/dbprovisioner/`, `agent/controller/system/runbookhook/`.

### Shared Commons (`common/`)
- Wire contract: `common/proto/transport.proto` — defines `PreConnect`, `Connect`, `HealthCheck`, `Packet{type, spec, payload}`.
- Generated code: `common/proto/transport.pb.go`, `transport_grpc.pb.go`.
- Protocol constants: `common/proto/agent/`, `common/proto/client/`, `common/proto/gateway/`, `common/proto/system/` — **always extend these constants rather than using string literals**.
- Shared utilities: `backoff/`, `grpc/`, `log/`, `memory/`, `envloader/`, `version/`, `license/`, `monitoring/`, `keys/`, `dsnkeys/`, `clientconfig/`, `featureflag/`, `agentcontroller/`, `aianalyzer/`, `apiutils/`, `appruntime/`, `httpclient/`, `runbooks/`.
- DB wire types: `pgtypes/`, `mssqltypes/`, `mongotypes/`.

### libhoop (`github.com/hoophq/libhoop`, private)
- One module. Protocol proxies (`agent/{postgres,mssql,mysql,mongodb,oracle}`, `proxy/ssh`), redactors and recorder at the root; the wire codecs under `v2/codec/{postgres,mysql,mssql,http}`.
- `v2/` is a plain directory, not a second module and not a major version. The import path reads `github.com/hoophq/libhoop/v2/codec/postgres` because that is where the files sit.
- libhoop is a **leaf**: it imports nothing from this repository. The wire vocabulary (`Statement`, `Operation`, `Column`, ...) is DEFINED in `v2/codec/types` and aliased by `sidecar/inspect`, so a codec satisfies `inspect.Codec` structurally without naming it. The SQL classifier and lexer stay in `sidecar/` and are injected into the codecs as func values.
- The codecs implement `sidecar/inspect`'s `Codec` interface and register through `inspect.Register`, so the module depends on `github.com/hoophq/hoop/sidecar`. That edge is one-way: nothing under `sidecar/` is imported by libhoop.
- Otherwise it **must not import from the main project** — bridge via stdlib types only.
- One build path: the proxy, in CI and locally. No stub, no symlink, no checkout step.
- Build produces WASM module for RDP parsing: `make generate-wasm`.

### Agent Rust (`agentrs/`)
- Rust binary for RDP proxy, TLS termination, WebSocket proxying.
- Source: `agentrs/src/` — `main.rs`, `lib.rs`, `run.rs`, `conf.rs`, `proxy.rs`, `rdp_proxy.rs`, `session.rs`, `tls.rs`, `x509.rs`, `piigate/` (PII masking), `ws/`.
- Cross-compile for dev: `make build-dev-rust` (uses `cross` for Linux targets from macOS).

### Tunnel (`tunnel/`)
- Client-side tunnel daemon (`hsh-tunneld`) that lets a developer reach any hoop connection by name (e.g. `psql -h pg-prod.hoop`) as if it were on the local network.
- Own Go module (`github.com/hoophq/hoop/tunnel`); entrypoint `tunnel/cmd/hsh-tunneld/`.
- Per TCP flow it opens a fresh gRPC `Transport.Connect` stream to the existing gateway — **no new gateway protocol/endpoint**; the gateway sees ordinary client sessions.
- Shipped with the unprivileged `hsh` CLI (separate `hoophq/hsh` repo).

### sidecar (`sidecar/`)
- Pure function over bytes: turns database wire-protocol bytes into statements, and statements into allow/deny verdicts. Opens no socket, terminates no TLS.
- The module root holds no Go files. `inspect/` is the core (bytes to statements, codec registry) and `daemon/` assembles the relay from config and carries the CLI entry point.
- Root module depends on `github.com/hoophq/libhoop` (private) and nothing else. Six nested modules isolate the heavier dependencies: `analyzer/vertex`, `cmd` (the `hoop-inspect` relay binary), `config/yaml`, `lexer/conformance`, `pii/alcatraz`, `store/sqlite`.
- CI reaches it through `make test-sidecar`, which `make test-oss` depends on: `go test github.com/hoophq/hoop/...` matches no module here, so the target walks every `go.mod` under `sidecar/` instead.
- Read `sidecar/CLAUDE.md` before changing anything under it; `sidecar/README.md` has the API and the relay/sidecar deployment.

## Transport Plugin System
Plugins are registered in `gateway/main.go` in a **fixed, intentional order** — do not reorder casually:
1. `review` (`gateway/transport/plugins/review/`)
2. `audit` (`gateway/transport/plugins/audit/`)
3. `dlp` (`gateway/transport/plugins/dlp/`)
4. `accesscontrol` / RBAC (`gateway/transport/plugins/accesscontrol/`)
5. `webhooks` (`gateway/transport/plugins/webhooks/`)
6. `slack` (`gateway/transport/plugins/slack/`)

Plugin interface: `gateway/transport/plugins/types/` — each plugin implements `OnStartup`, `Name`, and lifecycle hooks.
gRPC stream interceptors, in order (`gateway/transport/server.go` → `NewGRPCServer()`): `sessionuuid` → `auth` → `tracing`. `accessrequest` is not an interceptor; it runs during packet handling in `gateway/transport/client.go`.

## Gateway ↔ Agent Compatibility

Gateways auto-deploy. Agents run on customer infrastructure and can stay old for a long time.

- Never make a breaking change to the gateway ↔ agent contract: `common/proto/transport.proto`, packet types, packet spec keys, and payload formats.
- Assume the peer is an older agent. New fields must be optional, with a safe default when absent.
- Add new packet types or spec keys instead of changing the meaning of existing ones. Keep the old path working.
- Never remove or rename a packet type, spec key, or payload field that a released agent sends or reads.
- Gate new behavior on an agent capability (`supports_*` keys in `gateway/broker/headers.go`) or on `pb.SpecAgentVersion`. Fail closed when the capability is absent.
- A change that an old agent cannot process is a `major` change. Call it out in the PR description.

## Gateway Proxy Servers
Protocol-specific proxy servers configured through `models.ServerMiscConfig` (stored in `private.serverconfig`, edited via `/api/serverconfig/misc`):
- **PostgreSQL proxy**: `gateway/proxyproto/postgresproxy/`
- **SSH proxy**: `gateway/proxyproto/sshproxy/`
- **HTTP proxy**: `gateway/proxyproto/httpproxy/`
- **SSM proxy**: `gateway/proxyproto/ssmproxy/` (attached as Gin route group)
- **RDP**: `gateway/rdp/` — includes WASM-based bitmap parser, IronRDP integration.
- **gRPC key proxy**: `gateway/proxyproto/grpckey/`
- **TLS termination**: `gateway/proxyproto/tlstermination/`

## Configuration
- **Env-first** via `gateway/appconfig/appconfig.go`. A malformed `API_URL`, Postgres URI, `AUTH_METHOD`, GCP credential JSON, TLS, SPIFFE, or `RDP_PII_GUARD_POLICY` stops startup.
- `RDP_PII_SNAPSHOT_INTERVAL`, `RDP_PII_SCORE_THRESHOLD`, and `EVENT_ROUTING_WORKERS` fall back to a default instead.
- Key env vars: `POSTGRES_DB_URI`, `API_URL`, `GRPC_URL`, `AUTH_METHOD`, `DLP_PROVIDER`, `DLP_MODE`, `GIN_MODE`.
- DLP providers: Presidio (`MSPRESIDIO_ANALYZER_URL`, `MSPRESIDIO_ANONYMIZER_URL`) or GCP (`GOOGLE_APPLICATION_CREDENTIALS_JSON`).
- Auth provider resolution: dynamic (`gateway/idp/core.go`): DB `server_auth_config` overrides env; providers are `local`, `oidc`, `saml` with 30-minute cached verifier instances.

## Database & Migrations
- SQL migrations live in `gateway/migrations/` and are embedded into the gateway binary (`go:embed` + golang-migrate iofs). `MIGRATION_PATH_FILES` is the only way to read them from disk.
- File-based migrations run first via `golang-migrate`, then Go-coded migrations run via `modelsbootstrap.RunGolangMigrations()`.
- Create new migrations: `migrate create -ext sql -dir gateway/migrations -seq <description>`.
- Always provide both `.up.sql` and `.down.sql`; test rollback with `migrate ... down 1`.
- **IMPORTANT**: Use the next free number, read from the directory listing — not from a count. The sequence has gaps (no `000074`-`000076`) and unpaired files (`000084` down-only, `000085` up-only).
- If `origin/main` took your number, rename yours higher during merge conflict resolution.

## Critical Dev Workflows
| Task | Command | Notes |
|------|---------|-------|
| Start Postgres | `make run-dev-postgres` | Uses `scripts/dev/run-postgres.sh`; skip if you have your own PG |
| Start Presidio (DLP) | `make run-dev-presidio` | Optional, for data masking dev |
| Run gateway + agent | `make run-dev` | Uses `scripts/dev/run.sh`; reads `.env` (copy `.env.sample` first) |
| Build dev CLI | `make build-dev-client` | Output: `$HOME/.hoop/bin/hoop` |
| Build webapp into gateway | `make build-dev-webapp` | Then rerun `make run-dev` |
| Run frontend dev (both) | `cd webapp_v2 && npm run dev:full` | Starts Vite (:5173) + shadow-cljs (:8280) together. CLJS edits require a browser hard-reload — Vite proxies the bundle and can't HMR it. |
| Run React dev only | `cd webapp_v2 && npm run dev` | Vite on :5173. CLJS routes are blank until shadow-cljs is started separately. |
| Build Rust agent (dev) | `make build-dev-rust` | Cross-compiles for Linux from macOS |
| Run tests (OSS) | `make test-oss` | Generates WASM and runs `test-sidecar` first |
| Run tests (enterprise) | `make test-enterprise` | `make test` runs both |
| Run `sidecar` tests only | `make test-sidecar` | Walks every `go.mod` under `sidecar/`, nested modules included |
| Regenerate OpenAPI | `make generate-openapi-docs` | After any API route/schema change |
| Format Swagger annotations | `make swag-fmt` | |
| Create new SQL migration | `migrate create -ext sql -dir gateway/migrations -seq name` | |
| Publish release | `make publish` | Requires GitHub CLI (`gh`) |

## External Integrations
- **PostgreSQL**: Default state store for the gateway; embedded PGlite is the alternative.
- **DLP**: Presidio or GCP; configured via env, consumed in agent terminal execution (`agent/controller/terminal.go`).
- **Review/Approval**: Slack (`gateway/slack/service.go`), Jira (`gateway/jira/`, `gateway/api/integrations/`).
- **AI Clients**: OpenAI-compatible (`openai`, `azure-openai`, `custom`) + Anthropic for session analysis (`gateway/aianalyzer/`).
- **Monitoring**: Sentry (error tracking), Honeycomb, Segment (analytics), Intercom.
- **Webhooks**: `gateway/transport/plugins/webhooks/`, configurable via API (`gateway/api/webhooks/`).

## Deployment
- Local compose: `deploy/docker-compose/docker-compose.yml`.
- Helm charts: `deploy/helm-chart/chart/agent/`, `deploy/helm-chart/chart/gateway/`.
- AWS CloudFormation templates: `deploy/aws/`.
- Docker images: `Dockerfile` (production), `Dockerfile.dev` (dev), `Dockerfile.tools` (agent tools).
- Adding or renaming an env var? See "Environment variables" under Coding Conventions — the chart must change in the same PR.

## Feature Flags & Experimental Code

When implementing a new feature, behavior change, or non-trivial code path, **ask the user whether it should be gated behind a feature flag**. If the user confirms, follow these steps:

1. **Register the flag** — add one entry to `catalog` in `common/featureflag/featureflag.go`:
   - Name: `<stability>.<snake_case_name>` (e.g. `experimental.ssh_multiplex`, `beta.new_proxy`).
   - `Default`: see step 3. `Stability: StabilityExperimental` (or `StabilityBeta`).
   - `Components`: list which binaries use it (`ComponentGateway`, `ComponentAgent`, `ComponentClient`).
   - No migrations or frontend changes are needed — the flag appears automatically at Settings → Experimental.

2. **Gate every code path** — wrap the new behavior so it only runs when the flag is on:
   - **Gateway**: `featureflag.IsEnabled(orgID, "experimental.my_feature")` (import `common/featureflag`).
   - **Agent**: `featureflagstate.IsEnabled("experimental.my_feature")` (import `agent/controller/featureflagstate`).
   - **Webapp**: check `feature_flags` from the `/serverinfo` response.
   - Always preserve the existing behavior in the `else` branch.

3. **Pick the default by blast radius** — a feature that only adds a new code path is born `Default: true`. A feature that changes existing behavior is born `false`, then flipped once it proves out. `experimental.rulepacks` and `beta.oracle_native` both did this.

4. **The flag is a kill switch, not a hiding place** — a customer who hits a problem turns the feature off at **Settings → Experimental** (`/settings/experimental`, admin only). It is not a way to ship unfinished work; see "No Hacks" test 3.

See `DEV.md` "Feature Flags" section for the full developer guide and file reference.

## Architecture Decision Records

Before a structural change — one that spans modules, is expensive to
reverse, changes the gateway↔agent wire contract, or picks between real
alternatives — check whether it needs an ADR under `docs/adr/`. Most
changes don't: a new protocol handler that follows the existing
`agent/controller/` pattern, a routine schema change, or a bug fix are
not ADR material.

See `docs/adr/README.md` for the full policy and worked examples from
this codebase.

## Coding Conventions

### No Hacks
"Hack" is not a useful word — one reader's workaround is another's correct error handling. Use these four tests instead. Each one has a visible failure mode, so a reviewer can point at the code and say which test it fails.

**1. Compatibility shims are allowed.** An external defect (upstream library bug, driver quirk, protocol violation by a client) often has no fix but a shim. Land it when all three hold:
   - It is isolated — one function or file, not spread through the call path.
   - A comment names the upstream cause and the version it applies to.
   - A test fails when upstream fixes the defect, so the shim gets deleted instead of outliving its reason.

**2. Do not depend on a condition the repo does not enforce.** A required call order, an unversioned upstream behavior, or an assumed data shape must be held up by a type, a guard, or a test. A comment describing the assumption does not enforce it. If you cannot enforce it, the code does not land.

**3. Incomplete work is fine when the incomplete path fails loudly.** Return an error, refuse to start, or fail a test. No empty struct, zero value, or `nil, nil` standing in for a path you did not implement. A log line is not loud enough — the caller must be unable to continue as if the work happened.

**4. Do not expand scope to reach a fix.** If the correct fix needs a module the request did not name, stop and report what is missing. Do not widen the change to get there.

"I could not do X because the repository has no support for Y" is a good answer. Adding the missing support correctly is a better one.

### Go
- Prefer env-based configuration; add new config fields to `gateway/appconfig/appconfig.go`.
- Use the structured logger from `common/log` — not `fmt.Println` or stdlib `log`.
- Protocol packet types **must** be constants from `common/proto/{agent,client,gateway,system}` — never raw strings.
- Agent controller handlers follow one-file-per-protocol in `agent/controller/`.
- Gateway API handlers follow one-package-per-domain in `gateway/api/` (e.g., `gateway/api/connections/`, `gateway/api/session/`).
- Models use GORM; each entity gets its own file in `gateway/models/`.
- Model functions in `gateway/` should take `*gorm.DB` as a parameter. Both patterns exist: ~126 functions take it, 44 files still read the `models.DB` global. Use the parameter in new code.
- Model functions in `gateway/` must propagate `gorm.ErrRecordNotFound`. Callers decide how to handle not-found. Older helpers in `gateway/models/connections.go` return `nil, nil` — do not copy that.
- `libhoop` must stay independent — no imports from `gateway/`, `agent/`, `client/`, or `common/`.
- The sidecar <-> libhoop edge runs ONE way. libhoop must never import `github.com/hoophq/hoop/...`; if a codec needs behaviour from `sidecar/` (the SQL classifier, the lexer), inject it as a func value through the codec's `Options` rather than adding an import. `sidecar/codec/*` is the registration seam that does the injecting.
- Services layer: Business logic lives in `gateway/services/` — keep models focused on data, services on business logic.

### Testing
- Run with `make test-oss` (sets `CGO_ENABLED=0`, outputs JSON).
- Tests live alongside source files (`_test.go` suffix).
- The `generate-wasm` and `test-sidecar` steps are prerequisites, and the Makefile handles them automatically.

### API Changes
- Add Swagger annotations (swag comments) on new/modified handlers.
- Run `make generate-openapi-docs` to regenerate `gateway/api/openapi/autogen/`.
- OpenAPI specs are served at `/api/openapiv2.json` and `/api/openapiv3.json`.

### Product Analytics Events
Events are business KPIs read in PostHog/Mixpanel. A dropped event fails silently — no compile error, no test failure, the metric just goes to zero. Treat them as a contract.
- Names are constants in `gateway/analytics/events.go`. Never a string literal.
- Emit via `api.TrackRequest(analytics.EventX)` in the route registration, or `analytics.Track*` in the handler.
- A new route that supersedes a tracked one MUST emit the same event. `hoop-exec-runbook` was lost this way: `POST /runbooks/exec` replaced the tracked `POST /plugins/runbooks/connections/:name/exec` without `TrackRequest`.
- Removing or renaming an `Event*` constant, or its last emission site, MUST be called out in the PR description with the affected dashboard and the successor event.
- When refactoring a route that emits an event, verify the `TrackRequest`/`Track*` call survives.

### Environment variables
- Whenever a new env var is added, removed, or renamed in `gateway/appconfig/appconfig.go` (or anywhere read via `os.Getenv` in the gateway), **update the helm chart in the same PR**:
  - `deploy/helm-chart/chart/gateway/templates/secret-configs.yaml` — pass-through entry, e.g. `MY_VAR: '{{ .Values.config.MY_VAR | default "..." }}'`.
  - `deploy/helm-chart/chart/gateway/values.yaml` — add an example or sensible default under the `config:` block (commented if optional).
  - `deploy/helm-chart/chart/gateway/README.md` — document the new var if it is user-facing.
- Same rule applies to the agent helm chart at `deploy/helm-chart/chart/agent/` for agent-side env vars.
- For deployments managed in the `infra` repo (sandbox, hoopcloud envs), open a follow-up PR there if the new var needs to be enabled per-environment.
- The local dev `.env.sample` should also be updated so contributors discover the new var.

### Versioning
- Semantic versioning: `MAJOR.MINOR.PATCH`.
- Version is injected at build time via `-ldflags` into `common/version`.
- PR preview builds are tagged `{PR_NUMBER}.0.0-g{SHORT_SHA}` (`.github/workflows/pullrequest-release.yml`).

### PR Labels
Every PR **must** have exactly one release label. Choose based on the nature of the change:

| Label | When to use |
|-------|-------------|
| `major` | Breaking changes that require a major version bump |
| `minor` | New features or non-breaking enhancements |
| `patch` | Bug fixes, performance improvements, or minor non-breaking changes |
| `skip-release` | No release needed: docs, CI/CD changes, internal refactors, test-only changes |

Examples:
- New API endpoint or feature → `minor`
- Fix a crash or incorrect behavior → `patch`
- Remove a public API or change wire format → `major`
- Update `CLAUDE.md`, fix a GitHub Actions workflow, rename a variable → `skip-release`

### Writing Style
Applies to commit messages, PR descriptions, code comments, and ticket comments.

- Write the least possible amount of words. More than 2 lines is too long.
- Use ASD-STE100 Simplified Technical English.
- Reference a ticket by its ID (`DEP-147`), never by URL. The repos are public — a Linear URL leaks the workspace and the ticket slug.

## Merge Conflict Resolution
When merging `main` into a feature branch:
1. Check for migration conflicts first — duplicate migration numbers cause build failures.
2. If the feature branch's migration was already applied to DB, remove the migration files (don't rename).
3. If not yet applied, rename to next available number.
4. For code conflicts, prefer keeping the feature branch's approach unless it's clearly superseded by main.

## Key File Reference
| What | Path |
|------|------|
| Go workspace | `go.work` |
| Gateway entrypoint | `gateway/main.go` |
| Agent entrypoint | `agent/main.go` |
| CLI entrypoint | `client/hoop.go` → `client/cmd/root.go` |
| Proto definition | `common/proto/transport.proto` |
| Packet constants | `common/proto/agent/`, `common/proto/client/`, `common/proto/gateway/` |
| Agent packet dispatch | `agent/controller/agent.go` → `processPacket()` |
| App config | `gateway/appconfig/appconfig.go` |
| Auth/IDP | `gateway/idp/core.go` |
| API route registration | `gateway/api/server.go` → `buildRoutes()` |
| Role definitions | `gateway/api/apiroutes/roles.go` |
| Plugin registration | `gateway/main.go` (search `RegisteredPlugins`) |
| SQL migrations | `gateway/migrations/` |
| ADR policy & index | `docs/adr/README.md` |
| Dev run script | `scripts/dev/run.sh` |
| Env sample | `.env.sample` |
| Webapp entry (legacy CLJS) | `webapp/src/webapp/core.cljs` |
| Webapp entry (React shell) | `webapp_v2/src/main.jsx` |
| Frontend migration context | `webapp_v2/CONTEXT_MIGRATION.md` |
| Frontend coding rules | `webapp_v2/CLAUDE.md` |
| Wire-inspection library rules | `sidecar/CLAUDE.md` |

## Frontend Migration in Progress

**The frontend is being migrated from ClojureScript (`webapp/`) to React (`webapp_v2/`).**

`webapp_v2` is a React Shell that wraps the legacy ClojureScript app: it provides the global shell (Sidebar, CommandPalette) while ClojureScript continues to render page content. Pages are migrated one by one to React until the ClojureScript bundle can be removed entirely.

### Before working on any frontend issue

1. **Read `webapp_v2/CONTEXT_MIGRATION.md`** — explains the architecture, the shell/bridge contracts (`window.hoopSetRoute`, `localStorage.react-shell`, etc.), routing split, and migration status.
2. **Read `webapp_v2/CLAUDE.md`** — contains all coding rules, styling guidelines (Mantine only, no Tailwind), store/service patterns, and gotchas that apply to every change in `webapp_v2/`.

These two files are the authoritative source of truth for frontend work. Do not skip them.

Additionally, read these before the specific task:
- **Building UI or adding a component** → also read `webapp_v2/COMPONENTS.md` (catalog of existing components, hooks, stores, services — check before creating anything new).
- **Migrating a CLJS page to React** → also read `webapp_v2/MIGRATION_CHECKLIST.md` (step-by-step process) and `webapp_v2/CLJS_PATTERNS.md` (CLJS → React pattern mapping).

### Quick orientation

- New React pages live in `webapp_v2/src/pages/`
- Shared components live in `webapp_v2/src/components/`
- Global state is managed by Zustand stores in `webapp_v2/src/stores/`
- The reference implementation for a migrated page is `webapp_v2/src/pages/Agents/`
- Stack: React 19, Vite, Mantine v8, Zustand, React Router v7, lucide-react

### Dev servers

Use `cd webapp_v2 && npm run dev:full` to launch both Vite and shadow-cljs
together (recommended). Individual targets:

| Service | Port | Command |
|---------|------|---------|
| Vite (React shell) | 5173 | `cd webapp_v2 && npm run dev` |
| shadow-cljs (CLJS) | 8280 | `cd webapp && npm run dev` |
| Gateway (backend) | 8009 | see `Makefile` |

Hot reload: CLJS/Tailwind edits do NOT propagate as HMR through the Vite
proxy — hard-reload the tab (Cmd+Shift+R) after a CLJS change; details in
`webapp_v2/README.md`.

A `.env` in `webapp_v2/` is optional — see `webapp_v2/README.md` (Environment
Variables). Same goes for `webapp/.env`: only override `SENTRY_DSN`,
`SEGMENT_WRITE_KEY` or `API_URL` if you need to (closure-defines in
`shadow-cljs.edn` already supply usable defaults).

## Team AI Workflow

Shared Claude Code config lives in `.claude/` (see `.claude/README.md` for
setup, hoop-specific worktree notes, and the daily flow). Conventions the
tooling enforces:

- One ticket = one worktree = one session (`claude --worktree <ticket-id>`)
- `/fix-ticket <ID>` runs the standard ticket→draft-PR flow; `/test-plan`
  generates the mandatory "How to test" PR section
- Branch names come from Linear's `branchName`; commits start with the ticket ID
- Go changes: `make test-oss` green before PR; `webapp_v2/`: `npm run lint`
  and `npm run build`
- Every PR is born draft
