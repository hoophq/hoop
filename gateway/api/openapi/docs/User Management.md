Users are active and assigned to the default organization when they signup. A user could be set to an inactive state preventing it from accessing the platform, however it’s recommended to manage the state of users in the identity provider.

- The `sub` claim is used as the main identifier of the user in the platform.
- The profile of the user is derived from the id_token claims `email` and `name`.

When a user authenticates for the first time, it performs an automatic signup that persist the profile claims along with it’s unique identifier.
​
### Groups

Groups allows defining who may access or interact with certain resources.

- For connection resources it’s possible to define which groups has access to a specific connection, this is enforced when the Access Control feature is enabled.
- For review resources, it’s possible to define which groups are allowed to approve an execution, this is enforced when the Review feature is enabled.

> This resource could be managed manually via Webapp or propagated by the identity provider via ID Token. In this mode, groups are sync when a user performs a login.

### Roles

- The `admin` group is a special role that grants full access to all resources

This role should be granted to users that are responsible for managing the Gateway. All other users are regular, meaning that they can access their own resources and interact with connections.
