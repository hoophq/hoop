# Hoop

[![Hoop Release](https://img.shields.io/github/v/release/hoophq/hoopcli.svg?style=flat)](https://github.com/hoophq/hoop/actions/workflows/release.yml)
[![Hoop Release](https://github.com/hoophq/hoop/actions/workflows/release.yml/badge.svg)](https://github.com/hoophq/hoop/actions/workflows/release.yml)
[![Hoop Api Release](https://github.com/hoophq/api/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/hoophq/api/actions/workflows/release.yml)
[![Hoop Webapp Release](https://github.com/runopsio/webapp/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/runopsio/webapp/actions/workflows/release.yml)

Connect private infra-stractures without the need of a VPN.

- [Public Documentation](https://hoop.dev/docs)

## Development

To start a development server it requires the following local tools:

- [Golang 1.21+](https://go.dev/doc/install)
- [Docker](https://docs.docker.com/engine/install/)
- [Clojure / Java](https://clojure.org/guides/install_clojure)
- [git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
- Permission to clone `hoophq/xtdb` repository
- Postgres Server (remote or local)
- [node / npm](https://nodejs.org/en/download) (optional)
 - Permission to clone `runopsio/webapp` repository
 - Permission to clone `hoophq/api` repository

### Start a postgres server

Start and bootstrap a postgres server in background

```sh
make run-dev-postgres
```

> If you already have a postgres server, you don't need to execute this step.

### Start the server

The configuration file to run the development server is located at `.env`. It contains all the defaults to start it, make sure to add the `PG_HOST` and `IDP_CLIENT_SECRET` configuration.

```sh
# edit .env file with your favorite editor
cp .env.sample .env
```

> The sample configuration file, by default uses auth0 with the client id `tViX9dJ4K1sZ5omBE8yTaTXZAnlJ2q7D`. Contact an administrator to obtain the credentials or edit the `.env` file and pass your preferred identity provider credentials.

```sh
make run-dev
```

#### Webapp Setup

If you want to run a development server with the webapp, run the command below before executing starting the dev server

```sh
./scripts/dev/build-webapp.sh
```

### Build Dev Client

By default versioned clients are builded to strict connect via TLS. In order to build a client that permits connecting to remote hosts without TLS, execute the instruction below:

```sh
# generate binary at $HOME/.hoop/bin/hoop
make build-dev-client
```

> Append `$HOME/.hoop/bin` to your `$PATH` in your profile to find commands when typing in your shell

### Clean Up

To clean up all data and build scripts

```sh
rm -rf $HOME/.hoop/dev
```

## Postgrest

This project uses [postgrest](https://postgrest.org/en/stable/) as an interface to Postgres, it allows creating api's based on tables schema and permissions.
The bootstrap process performs all the necessary setup to make the postgrest fully functional, it consists in three steps:

- Perform the migration process using the [go-migrate library](https://github.com/golang-migrate/migrate)
- Provisioning the roles and permissions required for postgrest to work properly. [See authentication.](https://postgrest.org/en/stable/references/auth.html)
- Running the postgrest process in background

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

> In case of adding new views, make sure to add the proper permissions in the bootstrap process of postgrest

### Migration Best Practices

- https://github.com/golang-migrate/migrate/blob/master/MIGRATIONS.md
