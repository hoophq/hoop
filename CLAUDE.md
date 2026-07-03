# CLAUDE.md

## Project Overview

This is a Go workspace for the hoop gateway, agent, and CLI. The codebase follows a monorepo-style structure using `go.work`, with six modules: `gateway/`, `agent/`, `client/`, `common/`, `libhoop/`, and `tunnel/`. There is also a Rust companion binary (`agentrs/`) for RDP/TLS proxy workloads, a legacy ClojureScript SPA (`webapp/`) - see `webapp/CLAUDE.md` for its own conventions, and a new React frontend (`webapp_v2/`) that is actively replacing it.

 `_libhoop/` is a symlink target; the build uses `ln -s _libhoop libhoop` (`make libhoop-map`) so Go sees the `libhoop` import path.

## Toolchain & Prerequisites

- Go >= 1.24, Rust + `cross` (for cross-compiled `agentrs`), Docker, Node/npm, Clojure/Java.
- PostgreSQL is **mandatory** for the gateway (`POSTGRES_DB_URI`).
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
- HTTP API: `gateway/api/server.go` — Gin framework, serves static UI at `/` and REST API at `/api/*`.
- Route registration: `r.<METHOD>(path, [RoleMiddleware], r.AuthMiddleware, [api.AuditMiddleware()], [analytics tracking], handler)
- Role middleware: `AdminOnlyAccessRole`, `ReadOnlyAccessRole`, or none (standard). See `gateway/api/apiroutes/roles.go`.
- Auth middleware: `gateway/api/apiroutes/auth.go`.
- Audit middleware: `gateway/api/middleware.go`.
- Database: `gateway/models/` (GORM-based, one file per entity).
- Storage v2: `gateway/storagev2/` — newer abstraction with typed client state.

- Services: `gateway/services/` — business logic layer.

### Agent (`agent/`)
- Entrypoint: `agent/main.go` → `Run()` or `RunV2()` (embedded/DSN mode).
- Pre-connect loop (`PreConnect` RPC) then long-lived `Connect` stream with exponential backoff/reconnect.
- Packet dispatch: **type-driven** in `agent/controller/agent.go` → `processPacket()` switch statement.
- Protocol handlers: `postgres.go`, `mysql.go`, `mssql.go`, `mongodb.go`, `ssh.go`, `tcp.go`, `httpproxy.go`, `ssm.go`, `terminal.go`, `terminal-exec.go`.
- System operations: `agent/controller/system/dbprovisioner/`, `agent/controller/system/runbookhook/`.

### Shared Commons (`common/`)
- Wire contract: `common/proto/transport.proto` — defines `PreConnect`, `Connect`, `HealthCheck`, `Packet{type, spec, payload}`.
- Generated code: `common/proto/transport.pb.go`, `transport_grpc.pb.go`.
- Protocol constants: `common/proto/agent/`, `common/proto/client/`, `common/proto/gateway/`, `common/proto/system/` — **always extend these constants rather than using string literals**.
- Shared utilities: `backoff/`, `grpc/`, `log/`, `memory/`, `envloader/`, `version/`, `license/`, `monitoring/`, `keys/`, `dsnkeys/`, `clientconfig/`.
- DB wire types: `pgtypes/`, `mssqltypes/`, `mongotypes/`.

### libhoop (`libhoop/` / `_libhoop/`)
- Standalone library; **must not import from the main project** — bridge via stdlib types only.
- Contains: `agent/` (agent-side logic), `proxy/` (SSH proxy), `redactor/` (data masking types), `recorder/` (session recording), `llog/`, `lerrors/`, `lmemory/`.
- Rust FFI modules: `libhoop/rust_modules/`.
- Build produces WASM module for RDP parsing: `make generate-wasm`.

### Agent Rust (`agentrs/`)
- Rust binary for RDP proxy, TLS termination, WebSocket proxying.
- Source: `agentrs/src/` — `main.rs`, `proxy.rs`, `rdp_proxy.rs`, `session.rs`, `tls.rs`, `ws/`.
- Cross-compile for dev: `make build-dev-rust` (uses `cross` for Linux targets from macOS).

### Tunnel (`tunnel/`)
- Client-side tunnel daemon (`hsh-tunneld`) that lets a developer reach any hoop connection by name (e.g. `psql -h pg-prod.hoop`) as if it were on the local network.
- Own Go module (`github.com/hoophq/hoop/tunnel`); entrypoint `tunnel/cmd/hsh-tunneld/`.
- Per TCP flow it opens a fresh gRPC `Transport.Connect` stream to the existing gateway — **no new gateway protocol/endpoint**; the gateway sees ordinary client sessions.
- Shipped with the unprivileged `hsh` CLI (separate `hoophq/hsh` repo).

## Transport Plugin System
Plugins are registered in `gateway/main.go` in a **fixed, intentional order** — do not reorder casually:
1. `review` (`gateway/transport/plugins/review/`)
2. `audit` (`gateway/transport/plugins/audit/`)
3. `dlp` (`gateway/transport/plugins/dlp/`)
4. `accesscontrol` / RBAC (`gateway/transport/plugins/accesscontrol/`)
5. `webhooks` (`gateway/transport/plugins/webhooks/`)
6. `slack` (`gateway/transport/plugins/slack/`)

Plugin interface: `gateway/transport/plugins/types/` — each plugin implements `OnStartup`, `Name`, and lifecycle hooks.
gRPC interceptors (ordered): `sessionuuid` → `auth` → `tracing` → `accessrequest` — see `gateway/transport/interceptors/`.

## Gateway Proxy Servers
Protocol-specific proxy servers configured via `server_misc_config`:
- **PostgreSQL proxy**: `gateway/proxyproto/postgresproxy/`
- **SSH proxy**: `gateway/proxyproto/sshproxy/`
- **HTTP proxy**: `gateway/proxyproto/httpproxy/`
- **SSM proxy**: `gateway/proxyproto/ssmproxy/` (attached as Gin route group)
- **RDP**: `gateway/rdp/` — includes WASM-based bitmap parser, IronRDP integration.
- **gRPC key proxy**: `gateway/proxyproto/grpckey/`
- **TLS termination**: `gateway/proxyproto/tlstermination/`

## Configuration
- **Env-first** via `gateway/appconfig/appconfig.go` — startup fails fast on invalid envs.
- Key env vars: `POSTGRES_DB_URI`, `API_URL`, `GRPC_URL`, `AUTH_METHOD`, `DLP_PROVIDER`, `DLP_MODE`, `GIN_MODE`.
- DLP providers: Presidio (`MS_PRESIDIO_ANALYZER_URL`, `MS_PRESIDIO_ANONYMIZER_URL`) or GCP (`GCP_DLP_JSON_CREDENTIALS`).
- Auth provider resolution: dynamic (`gateway/idp/core.go`): DB `server_auth_config` overrides env; providers are `local`, `oidc`, `saml` with 30-minute cached verifier instances.

## Database & Migrations
- SQL migrations live in `rootfs/app/migrations/`.
- File-based migrations run first via `golang-migrate`, then Go-coded migrations run via `modelsbootstrap.RunGolangMigrations()`.
- Startup requires at least `000001_init.up.sql` to exist at the configured migration path.
- Create new migrations: `migrate create -ext sql -dir rootfs/app/migrations -seq <description>`.
- Always provide both `.up.sql` and `.down.sql`; test rollback with `migrate ... down 1`.

- **IMPORTANT**: Migration numbering must to be sequential. Check existing migrations before creating new ones to avoid conflicts. If a migration with the same number already exists in `origin/main`, it migration needs to be renamed to a higher number during merge conflict resolution.

## Critical Dev Workflows
| Task | Command | Notes |
|------|---------|-------|
| Start Postgres | `make run-dev-postgres` | Uses `scripts/dev/run-postgres.sh`; skip if you have your own PG |
| Start Presidio (DLP) | `make run-dev-presidio` | Optional, for data masking dev |
| Run gateway + agent | `make run-dev` | Uses `scripts/dev/run.sh`; reads `.env` (copy `.env.sample` first) |
| Build dev CLI | `make build-dev-client` | Output: `$HOME/.hoop/dev/hoop` (plaintext-friendly; the `bin/` sibling is reserved for `hoop versions`' active symlink) |
| Build webapp into gateway | `make build-dev-webapp` | Then rerun `make run-dev` |
| Run frontend dev (both) | `cd webapp_v2 && npm run dev:full` | Starts Vite (:5173) + shadow-cljs (:8280) together. CLJS edits require a browser hard-reload — Vite proxies the bundle and can't HMR it. |
| Run React dev only | `cd webapp_v2 && npm run dev` | Vite on :5173. CLJS routes are blank until shadow-cljs is started separately. |
| Build Rust agent (dev) | `make build-dev-rust` | Cross-compiles for Linux from macOS |
| Run tests | `make test-oss` | Auto-links `libhoop` and generates WASM first |
| Regenerate OpenAPI | `make generate-openapi-docs` | After any API route/schema change |
| Format Swagger annotations | `swag fmt` | Run in `gateway/` |
| Create new SQL migration | `migrate create -ext sql -dir rootfs/app/migrations -seq name` | |
| Publish release | `make publish` | Requires GitHub CLI (`gh`) |

## External Integrations
- **PostgreSQL**: Mandatory state store for gateway.
- **DLP**: Presidio or GCP; configured via env, consumed in agent terminal execution (`agent/controller/terminal.go`).
- **Review/Approval**: Slack (`gateway/slack/service.go`), Jira (`gateway/jira/`, `gateway/api/integrations/`).
- **AI Clients**: OpenAI + Anthropic for session analysis (`gateway/aiclients/`).
- **Monitoring**: Sentry (error tracking), Honeycomb, Segment (analytics), Intercom.
- **Webhooks**: `gateway/transport/plugins/webhooks/`, configurable via API (`gateway/api/webhooks/`).

## Deployment
- Local compose: `deploy/docker-compose/docker-compose.yml`.
- Helm charts: `deploy/helm-chart/chart/agent/`, `deploy/helm-chart/chart/gateway/`.
- AWS CloudFormation templates: `deploy/aws/`.
- Docker images: `Dockerfile` (production), `Dockerfile.dev` (dev), `Dockerfile.tools` (agent tools).

## Feature Flags & Experimental Code

When implementing a new feature, behavior change, or non-trivial code path, **ask the user whether it should be gated behind a feature flag**. If the user confirms, follow these steps:

1. **Register the flag** — add one entry to `catalog` in `common/featureflag/featureflag.go`:
   - Name: `<stability>.<snake_case_name>` (e.g. `experimental.ssh_multiplex`, `beta.new_proxy`).
   - `Default: false`, `Stability: StabilityExperimental` (or `StabilityBeta`).
   - `Components`: list which binaries use it (`ComponentGateway`, `ComponentAgent`, `ComponentClient`).
   - No migrations or frontend changes are needed — the flag appears automatically in the admin UI.

2. **Gate every code path** — wrap the new behavior so it only runs when the flag is on:
   - **Gateway**: `featureflag.IsEnabled(orgID, "experimental.my_feature")` (import `common/featureflag`).
   - **Agent**: `featureflagstate.IsEnabled("experimental.my_feature")` (import `agent/controller/featureflagstate`).
   - **Webapp**: check `feature_flags` from the `/serverinfo` response.
   - Always preserve the existing behavior in the `else` branch.

3. **No ungated experimental code on `main`** — every PR that adds experimental behavior must gate it. Default is always `false`, so a fresh deployment has everything off.

See `DEV.md` "Feature Flags" section for the full developer guide and file reference.

## Coding Conventions

### Go
- Prefer env-based configuration; add new config fields to `gateway/appconfig/appconfig.go`.
- Use the structured logger from `common/log` — not `fmt.Println` or stdlib `log`.
- Protocol packet types **must** be constants from `common/proto/{agent,client,gateway,system}` — never raw strings.
- Agent controller handlers follow one-file-per-protocol in `agent/controller/`.
- Gateway API handlers follow one-package-per-domain in `gateway/api/` (e.g., `gateway/api/connections/`, `gateway/api/session/`).
- Models use GORM; each entity gets its own file in `gateway/models/`.
- Model functions in `gateway/` must receive `*gorm.DB` as a parameter — never use a global DB variable.
- Model functions in `gateway/` must propagate `gorm.ErrRecordNotFound` to the caller — never swallow it by returning `nil, nil`. Let callers decide how to handle not-found cases.
- `libhoop` must stay independent — no imports from `gateway/`, `agent/`, `client/`, or `common/`.
- Services layer: Business logic lives in `gateway/services/` — keep models focused on data, services on business logic.

### Testing
- Run with `make test-oss` (sets `CGO_ENABLED=0`, outputs JSON).
- Tests live alongside source files (`_test.go` suffix).
- The `libhoop-map` and `generate-wasm` steps are prerequisites — the Makefile handles them automatically.

### API Changes
- Add Swagger annotations (swag comments) on new/modified handlers.
- Run `make generate-openapi-docs` to regenerate `gateway/api/openapi/autogen/`.
- OpenAPI specs are served at `/api/openapiv2.json` and `/api/openapiv3.json`.

### Product Analytics Events
Analytics events are business KPIs measured downstream in PostHog/Mixpanel. Dropping or orphaning one fails **silently** — no compile error, no test failure, just a metric that quietly goes to zero. Treat them as a contract, not as incidental code.
- Event names are constants in `gateway/analytics/events.go`. Emit them either via the `api.TrackRequest(analytics.EventX)` middleware in the route registration (`gateway/api/server.go`) or via `analytics.Track*` inside the handler. Never use a string literal — always the constant.
- **When you add, duplicate, or supersede a route that performs an already-tracked action** (exec, session create, connect, resource CRUD), carry the same tracking onto the new route. A new path that replaces a tracked one MUST emit the same event (or a documented successor) — otherwise the metric silently dies on the old path. This is exactly how `hoop-exec-runbook` was lost: the V2 `POST /runbooks/exec` route superseded the tracked `POST /plugins/runbooks/connections/:name/exec` without `TrackRequest`.
- **Never remove or rename an `Event*` constant, or delete its only emission site, without calling it out explicitly in the PR description** — state which metric/dashboard is affected and the successor event, if any. Event removal must be a deliberate, reviewed decision, never a side effect of a refactor.
- When reviewing or refactoring routes/handlers that emit events, verify the `TrackRequest`/`Track*` call is preserved.

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
- PR preview builds are tagged `{PR_NUMBER}.0.1-{SHORT_SHA}`.

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
| SQL migrations | `rootfs/app/migrations/` |
| Dev run script | `scripts/dev/run.sh` |
| Env sample | `.env.sample` |
| Webapp entry (legacy CLJS) | `webapp/src/webapp/core.cljs` |
| Webapp entry (React shell) | `webapp_v2/src/main.jsx` |
| Frontend migration context | `webapp_v2/CONTEXT_MIGRATION.md` |
| Frontend coding rules | `webapp_v2/CLAUDE.md` |

## Coding

NO HACKS. The user is EXTREMELY concerted about code quality much more than immediate results. If they ask you to build something and, while doing so, you hit a wall, and realize that the only way to ship the requested feature is to introduce a local hack, workaround, monkey patch, duct tap - STOP IMMEDIATLY. Either fix the underlying flaw that blocked you ina ROBUST, WELL DESIGNED, PRODUCTION READY manner, or be honest that the prompt can't be completed without hacks.

- DO NOT INTRODUCE HACKS IN THE CODE BASE
- DO NOT COMMIT CODE THAT COULD BREAK THINGS LATER
- DO NOT COMMIT PARTIAL SOLUTIONS OR WORKAROUNDS.

The author appreciates honestly and he WILL be glad and thankful if you respond a request with "I couldn't complete your request because the repository lacked support for X". He will be even happier if you go ahead and update the repo to provide necessary support in a well designed and robust way. But he will be VERY ANGRY if, while attempting to implement a feature, you introduce a workaround that will potentially break things latter.

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

Hot reload: Vite HMRs React sources instantly. shadow-cljs rebuilds
`/js/app.js` and `/css/site.css`, which Vite **proxies** — so CLJS/Tailwind
edits do NOT propagate as HMR into the running page. Hard-reload the tab
(Cmd+Shift+R) after a CLJS change.

A `.env` in `webapp_v2/` is optional — `vite.config.js` defaults all dev
proxy targets. Same goes for `webapp/.env`: only override `SENTRY_DSN`,
`SEGMENT_WRITE_KEY` or `API_URL` if you need to (closure-defines in
`shadow-cljs.edn` already supply usable defaults).
