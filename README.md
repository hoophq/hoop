![hero](github.png)

<h1 align="center"><b>hoop.dev</b></h1>
<p align="center">
    Access databases and servers with zero security compromises
    <br />
    <br />
    <a target="_blank" href="https://hoop.dev">Website</a>
    ·
    <a target="_blank" href="https://hoop.dev/docs">Docs</a>
    ·
    <a href="https://github.com/hoophq/hoop/discussions">Discussions</a>
  </p>
</p>


<p align="center">
    <a href="https://github.com/hoophq/hoop/actions/workflows/release.yml">
        <img src="https://img.shields.io/github/v/release/hoophq/hoopcli.svg?style=flat" />
    </a>
    <a href="https://github.com/hoophq/hoop/actions/workflows/release.yml">
        <img src="https://github.com/hoophq/hoop/actions/workflows/release.yml/badge.svg" />
    </a>
    <a href="https://github.com/hoophq/hoop/actions/workflows/release.yml">
        <img src="https://github.com/hoophq/api/actions/workflows/release.yml/badge.svg?branch=main" />
    </a>
    <a href="https://github.com/runopsio/webapp/actions/workflows/release.yml">
        <img src="https://github.com/runopsio/webapp/actions/workflows/release.yml/badge.svg?branch=main" />
    </a>
</p>


## About hoop.dev

Hoop.dev is an access gateway for databases and servers. Our AI-powered automations kill access policies and break-glass workflows with zero security compromises. How? Zero-config DLP policies with our AI Data Masking. Just-in-Time reviews by command and by access time. Hoop delivers security features like we are not even there for the developer. But present all the time for managers and auditors. That’s why great companies choose hoop.dev to provide secure access to anything. From databases to command line interfaces with Kubernetes or any server in any way.

## Host & Deploy

Host & Deploy hoop.dev with Kubernetes or at AWS using AWS Stack

 - [See Kubernets Deploy & Host Documentation](https://hoop.dev/docs/deploy/kubernetes)
 - [See AWS Deploy & Host Documentation](https://hoop.dev/docs/deploy/AWS)

## Development

To start a development server it requires the following local tools:

- [Golang 1.21+](https://go.dev/doc/install)
- [Docker](https://docs.docker.com/engine/install/)
- [Clojure / Java](https://clojure.org/guides/install_clojure)
- [git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
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

### Deployment

The deployment is done with [github action self hosted](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/about-self-hosted-runners). The production Kubernetes cluster has the namespaces `arc-system` and `arc-runners` which runs the deployment workflows.

The command below will show an interactive prompt and show the releases and apps available to deploy. It's important to follow up the deploy until it finishes.

#### Deploy By App

```sh
make deploy-by-app
```

#### Deploy All Instances

```sh
make deploy-all
```
