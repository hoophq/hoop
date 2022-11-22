# Hoop

Connect private infra-stractures without the need of a VPN.

- [Public Documentation](https://docs.runops.io/docs)

## Introduction

This repositories contains the codebase of three components required to forward TCP connections.

### Client

This is a command line utility used by the users/developers. It binds a local port and forward the packets to a remote infrastructure.

```sh
hoop mysql mysql-prod --proxy-port 3309
```

### Agent

The agent manages TCP connections and connect with the real services like MySQL, Postgres, Shell environments.

```sh
hoop-agent
```

### Gateway

The Gateway manages connections and users that are allowed to access resources. It interconnect proxies in a secure way.

## Gateway Configuration

To facilitate the deployment, it's possible to configure most of options using environment variables

| ENVIRONMENT                          | DESCRIPTION                                | AGENT | CLIENT | GATEWAY  |
| ------------------------------------ | ------------------------------------------ | ----- | ------ | -------- |
| XTDB_ADDRESS                         | Database server address                    | no    | no     | yes      |
| STATIC_UI_PATH                       | The path where the UI assets resides       | no    | no     | yes      |
| PROFILE                              | "dev" runs gateway without authentication  | no    | no     | yes      |
| GOOGLE_APPLICATION_CREDENTIALS_JSON  | GCP DLP credentials                        | no    | no     | yes      |

To customize the identity provider, the following variables can be set:

| ENVIRONMENT             | DESCRIPTION                                 | AGENT | CLIENT | GATEWAY |
| ----------------------- | ------------------------------------------- | ----- | ------ | ------- |
| API_URL                 | The public address of the API               | no    | no     | yes     |
| IDP_ISSUER              | The issuer of the application               | no    | no     | yes     |
| IDP_CLIENT_ID           | The oauth2 client  id                       | no    | no     | yes     |
| IDP_CLIENT_SECRET       | The oauth2 client secret                    | no    | no     | yes     |
| IDP_AUDIENCE            | The audience name                           | no    | no     | yes     |

## Development QuickStart

> Need golang and docker to start the development environment

Execute the command below to start the database, gateway and the agent

```sh
./scripts/run-dev.sh
```

To test the client:

```sh
./scripts/run-client-dev.sh -h
```

> The commands are compiled on the fly, thus any changes in the *.go files will be reflected executing the scripts.
