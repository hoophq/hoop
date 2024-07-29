This endpoint performs ad-hoc executions. It will wait 50 seconds for a sucessful response (200), otherwise return an Accepted status code (202) meaning the execution will be held asynchronously. The outcome could be obtained later on by fetching the resource using the attribute `id`.

The payload of this request is used with the Connection resource to construct the command to be executed in the remote agent.
- The `script` attribute is passed as stdin to the Connection resource `command` attribute.
- The attribute `client_args` is appended to the suffix of the `command`.

For example, the following connection:

```json
{
  "name": "bash-connection",
  "command": ["/bin/bash"],
  "type": "custom"
}
```

With the following payload:

```json
{
  "script": "echo 'hello world'",
  "client_args": ["-x"],
  "connection": "bash-connection"
}
```

Will perform an ad-hoc shell execution as:

```sh
/bin/bash -x <<EOF
echo 'hello world'
EOF
```
