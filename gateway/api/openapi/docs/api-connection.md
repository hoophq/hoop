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

### Tags

Tags are key/value pairs that are attached to objects such as Connections. Tags are intended to be used to specify identifying attributes of objects that are meaningful and relevant to users, but do not directly imply semantics to the core system.

```json
{
    "connection_tags": {
        "environment": "production",
        "component": "backend"
    }
}
```

Equality- or inequality-based requirements allow filtering by tags keys and values. Matching objects must satisfy all of the specified tag constraints, though they may have additional tags as well. Three kinds of operators are admitted `=`,`!=`. The first represent equality, while the last represents inequality. For example:

```
environment = production
tier != frontend
```

The former selects all resources with key equal to `environment` and value equal to `production`. The latter selects all resources with key equal to `tier` and value distinct from `frontend`. One could filter for resources in production excluding frontend using the comma operator: environment=production,tier!=frontend
