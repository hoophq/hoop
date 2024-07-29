The connection resource allows exposing internal services from your internal infra structure to users.

### Types of Connections

The definition of this resource represent how clients will be able to interact with internal resources.

Each type/subtype may represent a distinct implementation:

- `application/<subtype>` - An alias to map distinct types of shell applications (e.g.: python, ruby, etc)
- `application/tcp` - Forward TCP connections

    This type requires the following environment variables:
    - `HOST`: ip or dns of the internal service
    - `PORT`: the port of the internal service

- `custom` - Any custom shell application
- `database/<subtype>` - Allow connecting to databases through multiple clients (Webapp, cli, IDE's)

Each `<subtype>` has distinct environment variables that are allowed to be configured, refer to our [documentation](https://hoop.dev/docs) for more information.
