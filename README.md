# Hoop

Connect private infra-stractures without the need of a VPN.

- [Public Documentation](https://hoop.dev/docs)

## Development

> Need golang and docker to start the development environment

Execute the command below to start the database, gateway and the agent

```sh
./scripts/run-dev.sh
```

It will compile a command line in `/tmp/hoop`, which could be used for end-to-end tests

```sh
/tmp/hoop (...)
```

---

Using a development release for testing

```sh
hoop start
```

To test locally with authentication, refer [to the documentation](https://hoop.dev/docs/configuring/auth-auth0)
