## Development

To start a development server it requires the following local tools:

- [Golang 1.22+](https://go.dev/doc/install)
- [Rust](https://www.rust-lang.org/tools/install)
- [Rust-Cross](https://github.com/cross-rs/cross)
- [Docker](https://docs.docker.com/engine/install/)
- [Clojure / Java](https://clojure.org/guides/install_clojure)
- [git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
- Postgres Server (remote or local)
- [node / npm](https://nodejs.org/en/download)

### Start a postgres server

Start and bootstrap a postgres server in background

```sh
make run-dev-postgres
```

> If you already have a postgres server, you don't need to execute this step.

### Start the server

The configuration file to run the development server is located at `.env`.

```sh
# edit .env file with your favorite editor
cp .env.sample .env
```

> The sample configuration file doesn't have a default identity provider. Make sure to configure one to be able to run this project.
> By default this command will build the agentrs and will run inside the docker

```sh
make run-dev
```

#### Webapp Setup

To build the Webapp into the gateway

```sh
make build-dev-webapp
```

### Build Dev Client

By default versioned clients are builded to strict connect via TLS. In order to build a client that permits connecting to remote hosts without TLS, execute the instruction below:

```sh
# generate binary at $HOME/.hoop/bin/hoop
make build-dev-client
```

> Append `$HOME/.hoop/bin` to your `$PATH` in your profile to find commands when typing in your shell

### Data Masking Setup

We use Presidio as the provider to redact sensitive data on Hoop. To run the Presidio server, you need to have Docker installed and running.

```sh
make run-dev-presidio
```

### SPIFFE Agent Setup

Hoop can authenticate agents with SPIFFE JWT-SVIDs instead of DSN tokens. For local development we ship a minter that generates a trust bundle, signing key, and a short-lived JWT on your workstation — no SPIRE server required. See [`documentation/setup/deployment/spiffe.mdx`](../documentation/setup/deployment/spiffe.mdx) for the full feature docs.

1. Mint the SPIFFE artifacts and patch `.env` with `HOOP_SPIFFE_*` vars (observe mode):

```sh
make run-dev-spiffe-prep
```

This writes a managed block into `.env`:

```sh
# <<<HOOP_SPIFFE_DEV>>>
HOOP_SPIFFE_MODE=observe
HOOP_SPIFFE_TRUST_DOMAIN=local.test
HOOP_SPIFFE_BUNDLE_FILE=/app/spiffe/bundle.jwks
HOOP_SPIFFE_AUDIENCE=http://127.0.0.1:8009
HOOP_SPIFFE_REFRESH_PERIOD=30s
# <<</HOOP_SPIFFE_DEV>>>
```

and emits three files under `dist/dev/spiffe/`:

- `priv.pem` — RSA signing key (reused across runs, so the bundle stays stable)
- `bundle.jwks` — JWKS trust bundle mounted into the gateway at `/app/spiffe/bundle.jwks`
- `agent.jwt` — JWT-SVID for `spiffe://local.test/agent/local-dev`, 24h TTL (re-minted every run)

2. Start (or restart) the gateway so it picks up the new `HOOP_SPIFFE_*` vars:

```sh
make run-dev
```

> If `run-dev` was already running before step 1, `Ctrl-C` and start it again. SPIFFE configuration is loaded at gateway boot.

3. In another terminal, start a host-side agent that authenticates with the minted JWT:

```sh
make run-dev-spiffe-agent
```

This script:

- rebuilds `$HOME/.hoop/bin/hoop` if source files under `agent/` or `common/clientconfig/` are newer than the binary
- copies `bundle.jwks` into the `hoopdev` container (where `/app/spiffe/` is mounted)
- reads `POSTGRES_DB_URI` from `hoopdev`'s env and seeds two rows in `hoopdevpg` (idempotent):
  - `private.agents` → a `spiffe-agent` row
  - `private.agent_spiffe_mappings` → maps `spiffe://local.test/agent/local-dev` to that agent
- launches the agent on your host with `HOOP_KEY_FILE=dist/dev/spiffe/agent.jwt`, `HOOP_GRPCURL=127.0.0.1:8010`

`Ctrl-C` stops only the agent; `run-dev` keeps running.

#### Overriding defaults

| Variable | Default | Where it's used |
|---|---|---|
| `HOOP_SPIFFE_TRUST_DOMAIN` | `local.test` | Embedded in the minted JWT and the gateway config |
| `HOOP_SPIFFE_ID` | `spiffe://local.test/agent/local-dev` | Subject of the minted JWT and the DB mapping |
| `HOOP_SPIFFE_AUDIENCE` | `http://127.0.0.1:8009` | `aud` claim the gateway enforces |
| `HOOP_SPIFFE_TTL` | `24h` | Lifetime of the minted JWT |
| `HOOPDEV_APP_CONTAINER` | `hoopdev` | Gateway container (bundle copy + `POSTGRES_DB_URI` source) |
| `HOOPDEV_DB_CONTAINER` | `hoopdevpg` | Postgres container where `psql` runs |

#### Re-minting / rotating the JWT

The minter always writes a fresh `agent.jwt` while reusing `priv.pem`/`bundle.jwks`. To rotate:

```sh
make run-dev-spiffe-prep         # new agent.jwt, same bundle
# agent picks it up on its next reconnect (Refresh() is called in the backoff loop)
```

To rotate the signing key too, delete `dist/dev/spiffe/priv.pem` before running prep, then restart the gateway so it refreshes the bundle.

#### Switching to enforce mode

Edit `.env` and change `HOOP_SPIFFE_MODE=observe` to `HOOP_SPIFFE_MODE=enforce`, then restart `run-dev`. In `enforce` mode the gateway rejects agents that don't present a valid SVID (DSN fallback is disabled).

## Swagger / OpenAPI

This project uses [swag](https://github.com/swaggo/swag) to generate the api documentation. Make sure to generate it every time a change is made in the API:

```sh
make generate-openapi-docs
```

The gateway will expose the the openapi spec at `/api/openapiv2.json` and `/api/openapiv3.json`. Use your favorite openapi ui to view the documentation:

- https://swagger.io/tools/swagger-ui/
- https://redocly.github.io/redoc/

### Running Migrations

Install the [golang migrate cli](https://github.com/golang-migrate/migrate/releases/tag/v4.16.2) in your local machine

1. In the root folder of the project, run the following command:

```sh
migrate create -ext sql -dir rootfs/app/migrations -seq my_new_change
```

2. Add the `up` and `down` migrations
3. Start the gateway and it will automatically apply all migrations
4. Test reverting the migration

```sh
migrate -database 'postgres://hoopdevuser:1a2b3c4d@127.0.0.1:5449/hoopdevdb?sslmode=disable' -path rootfs/app/migrations/ down 1
```

### Migration Best Practices

- https://github.com/golang-migrate/migrate/blob/master/MIGRATIONS.md

### How to create a new release

1. After your pull request is merged into the main branch, update your local repository by pulling the latest changes:
```sh
git checkout main
git pull origin main
```

2. Run the release command:
```sh
make publish
```
Note: This step requires the GitHub CLI (gh) installed and authenticated with your GitHub account.

3. The release process automatically gathers all commits added since the last release and includes them in 
the new release notes.

4. Versioning rules `<version>.<minor>.<patch>`
- VERION (X.0.0) – Increment when you make incompatible API changes.
- MINOR (0.X.0) – Increment when you add functionality in a backward-compatible way.
- PATCH (0.0.X) – Increment when you make backward-compatible bug fixes.

### Development Production Builds

Every new commit in a Pull Request generates a production build for testing purporses.
The assets are tagged with the following semantic version:

- `{PR_NUMBER}.0.0-{PR_GIT_SHA_SHORT}`

> SHA_SHORT is the first 8 characters of the commit

To test it, access the [releases page](https://github.com/hoophq/hoop/releases) to see all the available assets.
Make sure to change the version of each asset with the new tag.

## Building Docker Agent Tools Image

The docker image agent tools is build manually. It requires Docker and [Docker Buildx](https://github.com/docker/buildx)

```sh
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -f Dockerfile.tools \
  --tag hoophq/agent-tools:noble-20251013 \
  --push .
```

