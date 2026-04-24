# CLAUDE.md

## Project Overview

This is a Go workspace for the hoop gateway, agent, and CLI. The codebase follows a monorepo-style structure using `go.work`.

 with five modules: `gateway/`, `agent/`, `client/`, `common/`, `libhoop/`. There is also a Rust companion binary (`agentrs/`) for RDP/TLS proxy workloads, a legacy ClojureScript SPA (`webapp/`) - see `webapp/CLAUDE.md` for its own conventions, and a new React frontend (`webapp_v2/`) that is actively replacing it.

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
| Build dev CLI | `make build-dev-client` | Output: `$HOME/.hoop/bin/hoop` (plaintext-friendly) |
| Build webapp into gateway | `make build-dev-webapp` | Then rerun `make run-dev` |
| Run React dev server | `cd webapp_v2 && npm run dev` | Vite on :5173; also start CLJS on :8280 |
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

## Coding Conventions

### Go
- Prefer env-based configuration; add new config fields to `gateway/appconfig/appconfig.go`.
- Use the structured logger from `common/log` — not `fmt.Println` or stdlib `log`.
- Protocol packet types **must** be constants from `common/proto/{agent,client,gateway,system}` — never raw strings.
- Agent controller handlers follow one-file-per-protocol in `agent/controller/`.
- Gateway API handlers follow one-package-per-domain in `gateway/api/` (e.g., `gateway/api/connections/`, `gateway/api/session/`).
- Models use GORM; each entity gets its own file in `gateway/models/`.
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

### Versioning
- Semantic versioning: `MAJOR.MINOR.PATCH`.
- Version is injected at build time via `-ldflags` into `common/version`.
- PR preview builds are tagged `{PR_NUMBER}.0.1-{SHORT_SHA}`.

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

## Frontend Migration in Progress

**The frontend is being migrated from ClojureScript (`webapp/`) to React (`webapp_v2/`).**

`webapp_v2` is a React Shell that wraps the legacy ClojureScript app: it provides the global shell (Sidebar, CommandPalette) while ClojureScript continues to render page content. Pages are migrated one by one to React until the ClojureScript bundle can be removed entirely.

### Before working on any frontend issue

1. **Read `webapp_v2/CONTEXT_MIGRATION.md`** — explains the architecture, the shell/bridge contracts (`window.hoopSetRoute`, `localStorage.react-shell`, etc.), routing split, and migration status.
2. **Read `webapp_v2/CLAUDE.md`** — contains all coding rules, styling guidelines (Mantine only, no Tailwind), store/service patterns, and gotchas that apply to every change in `webapp_v2/`.

These two files are the authoritative source of truth for frontend work. Do not skip them.

### Quick orientation

- New React pages live in `webapp_v2/src/pages/`
- Shared components live in `webapp_v2/src/components/`
- Global state is managed by Zustand stores in `webapp_v2/src/stores/`
- The reference implementation for a migrated page is `webapp_v2/src/pages/Agents/`
- Stack: React 19, Vite, Mantine v8, Zustand, React Router v7, lucide-react

### Dev servers

| Service | Port | Command |
|---------|------|---------|
| Vite (React shell) | 5173 | `cd webapp_v2 && npm run dev` |
| shadow-cljs (CLJS) | 8280 | `cd webapp && npm run dev` |
| Gateway (backend) | 8009 | see `Makefile` |
