## Development

To start a development server it requires the following local tools:

- [Golang 1.22+](https://go.dev/doc/install)
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

```sh
make run-dev
```

#### Webapp Setup

To build the webapp into the gateway

```sh
WEBAPP_BUILD=1 make run-dev
```

### Build Dev Client

By default versioned clients are builded to strict connect via TLS. In order to build a client that permits connecting to remote hosts without TLS, execute the instruction below:

```sh
# generate binary at $HOME/.hoop/bin/hoop
make build-dev-client
```

> Append `$HOME/.hoop/bin` to your `$PATH` in your profile to find commands when typing in your shell

## Swagger / OpenAPI

This project uses [swag](https://github.com/swaggo/swag) to generate the api documentation. Make sure to generate it every time a change is made in the API:

```sh
make generate-openapi-docs
```

The gateway will expose the swagger at `$API_URL/api/openapiv2.json`, use your favorite openapi ui to view the documentation:

- https://swagger.io/tools/swagger-ui/
- https://redocly.github.io/redoc/

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
