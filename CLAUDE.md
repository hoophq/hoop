# Hoop - Development Guidelines

## Project Overview

Hoop is an open-source security gateway that sits between users/AI agents and infrastructure (databases, Kubernetes, SSH, APIs). Monorepo with Go, Rust, and ClojureScript.

## Repository Structure

```
gateway/       - Core gateway server (Go, Gin framework)
agent/         - Agent that connects to gateway via gRPC (Go)
client/        - CLI client (Go, Cobra framework)
common/        - Shared libraries and protobuf definitions (Go)
webapp/        - Web UI (ClojureScript, React, Shadow-cljs)
agentrs/       - Rust agent (Tokio, Axum, RDP protocol)
_libhoop/      - Protocol libraries (Go)
deploy/        - Docker Compose, Helm charts, AWS CloudFormation
rootfs/        - Container filesystem, SQL migrations
scripts/       - Build and utility scripts
```

## Go Workspace

Uses Go 1.24+ workspaces (`go.work`) with modules: `./agent`, `./client`, `./common`, `./gateway`, `./libhoop`.

## Build Commands

```bash
make build-go              # Build Go binaries (gateway + client)
make build-rust-single     # Build Rust binaries
make build-webapp          # Build ClojureScript webapp
make build-dev-client      # Development client build
make build-dev-rust        # Development Rust build
make generate-wasm         # Generate WASM for RDP parser
make generate-openapi-docs # Generate Swagger/OpenAPI docs
```

## Test Commands

```bash
make test                  # Run all Go tests (CGO_ENABLED=0)
make test-oss              # Run OSS tests only
make test-enterprise       # Run enterprise tests only
```

Tests use native Go `testing` package. All tests run with `CGO_ENABLED=0`.

## Development Setup

```bash
make run-dev-postgres      # Start PostgreSQL locally
make run-dev               # Start gateway with Docker
make build-dev-client      # Build CLI client
make build-dev-webapp      # Build UI
make run-dev-presidio      # Start data masking service (Presidio)
```

Configuration via environment variables (see `.env.sample`).

## Code Style

### Go
- Standard `gofmt` formatting
- Package names follow Go conventions (lowercase, single word)
- Kebab-case for connection types, snake_case for DB columns
- Error handling: return errors up the call stack, wrap with context

### Rust (agentrs)
- Standard `rustfmt` / `clippy`
- Async with Tokio runtime
- Cross-compilation via `cross-rs`

### ClojureScript (webapp)
- See `webapp/CLAUDE.md` for detailed webapp guidelines
- Shadow-cljs build system, Tailwind CSS, Radix UI components
- re-frame for state management

## Architecture Highlights

- **Protocol Proxies**: Wire-protocol parsing at gateway layer (PostgreSQL, MySQL, MSSQL, MongoDB, SSH, RDP, HTTP/gRPC, AWS SSM)
- **Transport**: gRPC between gateway and agents, WebSocket for real-time UI
- **Auth**: JWT, OIDC, SAML, local auth
- **Security**: Data masking (Presidio ML), guardrails, approval workflows, session recording
- **AI Integration**: Anthropic SDK, OpenAI SDK
- **Database**: PostgreSQL with golang-migrate migrations (`rootfs/app/migrations/`)

## Key Directories

- `gateway/api/` - REST API handlers (35+ packages: connections, sessions, guardrails, agents, etc.)
- `gateway/pgrest/` - PostgreSQL REST layer
- `gateway/transport/` - Protocol proxy plugins
- `gateway/security/` - Auth and security middleware
- `agent/controller/` - Agent connection and database management
- `client/cmd/` - CLI command definitions
- `common/proto/` - Protobuf/gRPC definitions

## CI/CD

- GitHub Actions for PRs: runs `make test-oss` with Go 1.24+ and Rust/WASM
- Release triggered by git tags (`*.*.*` pattern)
- Multi-platform builds: Linux/Darwin, amd64/arm64
- Docker images: `hoophq/hoop` (gateway), `hoophq/hoopdev` (dev agent)
