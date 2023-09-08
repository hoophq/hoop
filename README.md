# Hoop

Connect private infra-stractures without the need of a VPN.

- [Public Documentation](https://hoop.dev/docs)

## Development

To start a development server it requires the following local tools:

- [Golang 1.21+](https://go.dev/doc/install)
- [Docker](https://docs.docker.com/engine/install/)
- [Clojure / Java](https://clojure.org/guides/install_clojure)
- [git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
- Permission to clone `hoophq/xtdb` repository
- Postgres Server (remote or local) (optional)
- [node / npm](https://nodejs.org/en/download) (optional)
 - Permission to clone `runopsio/webapp` repository

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
